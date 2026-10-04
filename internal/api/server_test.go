package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// newTestServer builds an API server without a database.
func newTestServer() *httptest.Server {
	log := zerolog.Nop()
	srv := NewServer(nil, log)
	ts := httptest.NewServer(srv.Handler())
	return ts
}

func TestHealth(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSignalsWithoutDB(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	for _, path := range []string{"/api/signals", "/api/signals/abc", "/api/recordings", "/api/sdrs"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", path, resp.StatusCode)
		}
	}
}

func TestUpdateSDRWithoutDB(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/sdrs/rtlsdr-0", strings.NewReader(`{"gainDb":40}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestWSRelayPending(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ws")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestCORSAllowlistedOrigin(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:5173")
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want http://localhost:5173", got)
	}
}

func TestCORSRejectsForeignOrigin(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:5173")
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none", got)
	}

	// Cross-origin preflights get no CORS grant: go-chi/cors aborts
	// them without CORS headers (browsers treat that as a failure).
	pre, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/signals", nil)
	pre.Header.Set("Origin", "http://evil.example")
	pre.Header.Set("Access-Control-Request-Method", http.MethodGet)
	presp, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	presp.Body.Close()
	if got := presp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("preflight Access-Control-Allow-Origin = %q, want none", got)
	}
	if got := presp.Header.Get("Access-Control-Allow-Methods"); got != "" {
		t.Fatalf("preflight Access-Control-Allow-Methods = %q, want none", got)
	}
}

func TestCORSDenyAllWhenEnvEmpty(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want none (deny-all)", got)
	}
}

func TestAudioContentType(t *testing.T) {
	cases := map[string]string{
		"wav":  "audio/wav",
		"iq":   "application/octet-stream",
		"flac": "application/octet-stream", // removed format must not resurface
		"":     "application/octet-stream",
	}
	for format, want := range cases {
		if got := audioContentType(format); got != want {
			t.Fatalf("audioContentType(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestUpdateSDRInvalidJSON(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	// Without a DB this should 503 before touching JSON; verify the
	// handler chain responds deterministically rather than panicking.
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/sdrs/rtlsdr-0", strings.NewReader(`{"gainDb":`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}