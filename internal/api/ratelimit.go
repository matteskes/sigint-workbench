// Package api — per-client REST rate limiting (Test R8, §4.10.10).
//
// The contract: the gateway enforces per-client token-bucket limits on
// its REST API; an abusive client gets 429 + Retry-After while other
// clients are unaffected. High-frequency polling is the gateway's main
// load and this is its only shared ingress, so without per-client
// limits one runaway UI or scraper starves the rest.
//
// Design notes:
//   - Client identity is the RemoteAddr IP. The gateway is the edge —
//     docker-compose publishes :8080 straight to it and nothing fronts
//     it — so X-Forwarded-For is untrusted here (a client-supplied XFF
//     would be an identity-spoofing knob, letting one client mint fresh
//     buckets at will). There is no auth by design (RX-only LAN tool,
//     §4.11), so source IP is the honest per-client key.
//   - Only /api/* is limited: the contract's scope is REST polling.
//     /health must stay both probe-free and limiter-free (compose
//     healthchecks and monitors poll it), and the /ws relays have their
//     own connection lifecycle (§14) — their handshakes are not the
//     load this contract targets.
//   - The bucket is a plain refillable token bucket: burst tokens,
//     refilled at rps tokens/second up to burst. Retry-After is the
//     ceil of the time until one token refills.
//   - The clock is injectable (now func() time.Time) so the tests are
//     deterministic — no sleeps, no real waiting.
//   - Buckets are evicted lazily (only when the map grows past
//     maxRateBuckets, and at most once per sweep interval): a large
//     LAN or a NAT'd fleet should not grow the map without bound.
package api

import (
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	// defaultRateRPS / defaultRateBurst size the limiter when RATE_LIMIT_*
	// are unset: high enough that the dashboard's polling, the settings
	// round-trips and the e2e suites never trip it (their busiest client
	// polls at ~4 Hz), low enough that a sustained burst-abuse test can
	// exhaust the bucket in one quick volley.
	defaultRateRPS   = 50
	defaultRateBurst = 100

	// maxRateBuckets bounds the bucket map before a sweep is considered;
	// rateBucketIdle is how long an idle bucket survives a sweep (a
	// client idle this long loses its accumulated tokens — it restarts
	// at burst, which is what a new client gets anyway). Sweeps run at
	// most once per rateSweepPeriod so the cost stays amortized.
	maxRateBuckets  = 4096
	rateBucketIdle  = 10 * time.Minute
	rateSweepPeriod = time.Minute
)

// rateLimiter is the per-key token bucket behind the gateway's REST
// rate limit. All methods are safe for concurrent use.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rps     float64
	burst   float64
	now     func() time.Time // injectable for deterministic tests
	sweptAt time.Time
}

// tokenBucket is one client's bucket. seen doubles as the refill
// timestamp and the idle-eviction timestamp.
type tokenBucket struct {
	tokens float64
	seen   time.Time
}

// newRateLimiter builds a limiter allowing burst requests instantly and
// rps requests/second sustained, per key.
func newRateLimiter(rps, burst float64, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*tokenBucket),
		rps:     rps,
		burst:   burst,
		now:     now,
	}
}

// newRateLimiterFromEnv reads RATE_LIMIT_RPS / RATE_LIMIT_BURST. An
// unset pair means the defaults; an explicit RATE_LIMIT_RPS=0 (or a
// burst below 1) disables limiting entirely (nil limiter — the
// middleware is not registered). Invalid values fall back to the
// defaults with a warning rather than failing startup: a typo in an
// env var must not take down the only client ingress (§13).
func newRateLimiterFromEnv(log zerolog.Logger) *rateLimiter {
	rps := float64(defaultRateRPS)
	if v := os.Getenv("RATE_LIMIT_RPS"); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			log.Warn().Str("value", v).Msg("RATE_LIMIT_RPS invalid, using default")
		} else {
			rps = n
		}
	}
	burst := float64(defaultRateBurst)
	if v := os.Getenv("RATE_LIMIT_BURST"); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			log.Warn().Str("value", v).Msg("RATE_LIMIT_BURST invalid, using default")
		} else {
			burst = n
		}
	}
	if rps <= 0 || burst < 1 {
		return nil
	}
	return newRateLimiter(rps, burst, time.Now)
}

// allow reports whether key may proceed now, and if not, how long until
// one token refills (for the Retry-After header).
func (rl *rateLimiter) allow(key string) (bool, time.Duration) {
	now := rl.now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if len(rl.buckets) >= maxRateBuckets && now.Sub(rl.sweptAt) >= rateSweepPeriod {
		rl.sweepLocked(now)
	}

	b := rl.buckets[key]
	if b == nil {
		b = &tokenBucket{tokens: rl.burst, seen: now}
		rl.buckets[key] = b
	} else if d := now.Sub(b.seen); d > 0 {
		b.tokens = math.Min(rl.burst, b.tokens+d.Seconds()*rl.rps)
	}
	b.seen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / rl.rps * float64(time.Second))
	return false, wait
}

// sweepLocked drops idle buckets. Caller holds mu.
func (rl *rateLimiter) sweepLocked(now time.Time) {
	for key, b := range rl.buckets {
		if now.Sub(b.seen) >= rateBucketIdle {
			delete(rl.buckets, key)
		}
	}
	rl.sweptAt = now
}

// rateLimit is the chi middleware. Registered for the whole router but
// self-limited to /api/* paths (see the package comment for why /health
// and /ws are exempt); a nil limiter means limiting is disabled.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.limiter != nil && strings.HasPrefix(r.URL.Path, "/api/") {
			ok, wait := s.limiter.allow(clientKey(r))
			if !ok {
				secs := int(math.Ceil(wait.Seconds()))
				if secs < 1 {
					secs = 1
				}
				s.log.Warn().
					Str("client", clientKey(r)).
					Str("path", r.URL.Path).
					Int("retry_after_s", secs).
					Msg("rate limit exceeded")
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey extracts the rate-limit identity: the RemoteAddr IP. The
// error path keeps the raw RemoteAddr (some test servers and unix
// sockets carry no port to split).
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
