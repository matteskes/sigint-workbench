// Package ws — WebSocket event hub and shared origin policy.
//
// The origin policy (SPEC §17.2) mirrors the gateway's CORS
// decision: cross-origin WebSocket upgrades are rejected unless the
// Origin is explicitly allowlisted via ALLOWED_ORIGINS.
package ws

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

// defaultOrigins are the frontend origins (nginx prod :3000,
// SvelteKit/Vite dev :5173).
var defaultOrigins = []string{"http://localhost:3000", "http://localhost:5173"}

// lookupEnv is indirected for tests.
var lookupEnv = os.LookupEnv

// AllowedOriginsFromEnv parses the comma-separated ALLOWED_ORIGINS
// environment variable. Unset falls back to the frontend origins;
// set-but-empty denies every cross-origin request (same-origin
// deployments behind the nginx proxy need no exceptions).
func AllowedOriginsFromEnv() []string {
	raw, ok := lookupEnv("ALLOWED_ORIGINS")
	if !ok {
		return append([]string(nil), defaultOrigins...)
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CheckOrigin reports whether a request with the given Origin header
// may proceed: requests without an Origin (non-browser clients:
// services, relays, tests) and same-origin requests are allowed;
// anything else must exactly match one of the allowed origins.
func CheckOrigin(allowed []string, r *http.Request, origin string) bool {
	if origin == "" {
		return true // non-browser client
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Host == r.Host {
		return true // same-origin
	}
	for _, a := range allowed {
		if a != "" && strings.EqualFold(strings.TrimSuffix(a, "/"), origin) {
			return true
		}
	}
	return false
}

// OriginCheckFunc returns a gorilla/websocket CheckOrigin policy
// backed by CheckOrigin.
func OriginCheckFunc(allowed []string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return CheckOrigin(allowed, r, r.Header.Get("Origin"))
	}
}