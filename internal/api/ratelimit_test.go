package api

// Hermetic tests for the per-client REST rate limit (R8, §4.10.10).
// The clock is faked, so burst exhaustion, refill, Retry-After math and
// idle eviction are all deterministic — no sleeps. The middleware tests
// build the Server struct directly (same pattern as server_db_test.go):
// with a nil DB, /api/signals answers 503 *after* passing the limiter,
// which cleanly distinguishes "limited" (429) from "allowed through".

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

// fakeClock is a deterministic time source: tests advance it manually.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func TestRateLimiterBurstExhaustionAndRefill(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	rl := newRateLimiter(10, 5, clock.Now) // 10 req/s sustained, burst 5

	// The burst passes instantly...
	for i := 0; i < 5; i++ {
		ok, wait := rl.allow("10.0.0.1")
		if !ok || wait != 0 {
			t.Fatalf("request %d of burst denied: ok=%v wait=%v", i+1, ok, wait)
		}
	}
	// ...then the bucket is dry: the next request waits for one refill.
	ok, wait := rl.allow("10.0.0.1")
	if ok {
		t.Fatal("request beyond burst was allowed")
	}
	if want := 100 * time.Millisecond; wait != want {
		t.Fatalf("wait = %v, want %v (1 token at 10 rps)", wait, want)
	}

	// Halfway to a refill: still dry.
	clock.Advance(50 * time.Millisecond)
	if ok, _ := rl.allow("10.0.0.1"); ok {
		t.Fatal("allowed before one refill period elapsed")
	}

	// Exactly one refill period: exactly one more request passes.
	clock.Advance(50 * time.Millisecond)
	if ok, _ := rl.allow("10.0.0.1"); !ok {
		t.Fatal("one token should have refilled after 100ms at 10 rps")
	}
	if ok, _ := rl.allow("10.0.0.1"); ok {
		t.Fatal("only one token should have refilled")
	}

	// A long idle stretch refills to the burst cap, not beyond it.
	clock.Advance(10 * time.Second)
	for i := 0; i < 5; i++ {
		if ok, _ := rl.allow("10.0.0.1"); !ok {
			t.Fatalf("request %d of refilled burst denied", i+1)
		}
	}
	if ok, _ := rl.allow("10.0.0.1"); ok {
		t.Fatal("refill must cap at burst")
	}
}

func TestRateLimiterPerClientIsolation(t *testing.T) {
	// Contract step 4 (§4.10.10), hermetic half: draining one client's
	// bucket must not touch any other client's.
	clock := &fakeClock{t: time.Unix(0, 0)}
	rl := newRateLimiter(5, 2, clock.Now)

	for i := 0; i < 2; i++ {
		if ok, _ := rl.allow("10.0.0.1"); !ok {
			t.Fatalf("abusive client burst request %d denied", i+1)
		}
	}
	if ok, _ := rl.allow("10.0.0.1"); ok {
		t.Fatal("abusive client should be drained")
	}
	if ok, _ := rl.allow("10.0.0.2"); !ok {
		t.Fatal("second client affected by first client's exhausted bucket")
	}

	// Two remote ports of the same IP share one bucket: the key is the
	// IP, not the socket.
	if ok, _ := rl.allow("10.0.0.2"); !ok {
		t.Fatal("same IP from a second port should share its bucket")
	}
	if ok, _ := rl.allow("10.0.0.2"); ok {
		t.Fatal("same IP from a second port should not mint fresh tokens")
	}
}

func TestRateLimiterIdleEviction(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	rl := newRateLimiter(10, 5, clock.Now)

	// Grow the map to the sweep threshold; every bucket is born "now".
	for i := 0; i < maxRateBuckets; i++ {
		rl.allow(strconv.Itoa(i))
	}
	if len(rl.buckets) != maxRateBuckets {
		t.Fatalf("bucket count = %d, want %d", len(rl.buckets), maxRateBuckets)
	}

	// Past the idle horizon, growth past the threshold triggers one
	// sweep: every stale bucket goes, the fresh one stays.
	clock.Advance(rateBucketIdle + time.Second)
	rl.allow("fresh")
	if got := len(rl.buckets); got != 1 {
		t.Fatalf("after sweep bucket count = %d, want 1", got)
	}
	if _, ok := rl.buckets["fresh"]; !ok {
		t.Fatal("the bucket that triggered the sweep must survive it")
	}

	// Sweeps are amortized: inside the sweep period the map may exceed
	// the threshold without a second O(n) walk.
	for i := 0; i < maxRateBuckets; i++ {
		rl.allow(strconv.Itoa(i))
	}
	if len(rl.buckets) != maxRateBuckets+1 {
		t.Fatalf("bucket count = %d, want %d (no second sweep inside period)",
			len(rl.buckets), maxRateBuckets+1)
	}
}

func TestRateLimitMiddleware429AndHeaders(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	srv := &Server{
		router:  chi.NewRouter(),
		log:     zerolog.Nop(),
		limiter: newRateLimiter(10, 3, clock.Now), // small: 3-request burst
	}
	srv.buildRoutes()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	url := ts.URL + "/api/signals"

	// The burst passes the limiter; with no DB the handler answers 503 —
	// the point is that it is NOT 429 (the limiter let it through).
	for i := 0; i < 3; i++ {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("burst request %d: %v", i+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("burst request %d: status = %d, want 503 (through limiter)",
				i+1, resp.StatusCode)
		}
	}

	// The 4th request is the limiter's: 429 + integer Retry-After ≥ 1.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("limited request: %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		resp.Body.Close()
		t.Fatalf("drained client: status = %d, want 429", resp.StatusCode)
	}
	ra := resp.Header.Get("Retry-After")
	n, err := strconv.Atoi(ra)
	resp.Body.Close()
	if err != nil || n < 1 {
		t.Fatalf("Retry-After = %q, want parseable integer ≥ 1", ra)
	}

	// Exempt paths still answer while the bucket is dry: /health (probe
	// traffic must never be limited) and any non-/api prefix (chi 404s
	// it — the limiter never saw the request).
	for _, path := range []string{"/health", "/signals"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("exempt path %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("exempt path %s answered 429", path)
		}
	}

	// Respecting Retry-After recovers the client (contract step 3):
	// advance the fake clock past the header's promise; the next request
	// passes the limiter again (503 from the DB-less handler, not 429).
	clock.Advance(time.Duration(n) * time.Second)
	resp, err = http.Get(url)
	if err != nil {
		t.Fatalf("post-recovery request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("post-recovery: status = %d, want 503 (limiter admitted it)",
			resp.StatusCode)
	}
}

func TestNewRateLimiterFromEnv(t *testing.T) {
	cases := []struct {
		name       string
		rps, burst string
		nilLimiter bool
		wantRPS    float64
	}{
		{name: "unset means defaults", rps: "", burst: "", wantRPS: defaultRateRPS},
		{name: "explicit pair", rps: "10", burst: "20", wantRPS: 10},
		{name: "fractional rps", rps: "2.5", burst: "4", wantRPS: 2.5},
		{name: "invalid rps falls back", rps: "abc", burst: "", wantRPS: defaultRateRPS},
		{name: "zero rps disables", rps: "0", burst: "100", nilLimiter: true},
		{name: "negative rps disables", rps: "-1", burst: "100", nilLimiter: true},
		{name: "zero burst disables", rps: "50", burst: "0", nilLimiter: true},
		{name: "invalid burst falls back", rps: "50", burst: "xyz", wantRPS: defaultRateRPS},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RATE_LIMIT_RPS", tc.rps)
			t.Setenv("RATE_LIMIT_BURST", tc.burst)
			rl := newRateLimiterFromEnv(zerolog.Nop())
			if tc.nilLimiter {
				if rl != nil {
					t.Fatalf("limiter = %+v, want nil (disabled)", rl)
				}
				return
			}
			if rl == nil {
				t.Fatal("limiter = nil, want enabled")
			}
			if rl.rps != tc.wantRPS {
				t.Fatalf("rps = %v, want %v", rl.rps, tc.wantRPS)
			}
			if rl.now == nil {
				t.Fatal("clock not wired")
			}
		})
	}
}

func TestClientKeyStripsPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/signals", nil)
	req.RemoteAddr = "192.168.1.7:53112"
	if got := clientKey(req); got != "192.168.1.7" {
		t.Fatalf("clientKey = %q, want bare IP", got)
	}
	req.RemoteAddr = "no-port-value"
	if got := clientKey(req); got != "no-port-value" {
		t.Fatalf("clientKey = %q, want raw RemoteAddr fallback", got)
	}
}
