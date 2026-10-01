package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"sigint-workbench/internal/ws"
)

// newTestServer builds an API server without a database.
func newTestServer() (*Server, *ws.Hub, *httptest.Server) {
	log := zerolog.Nop()
	hub := ws.NewHub()
	go hub.Run()
	srv := NewServer(nil, hub, log)
	ts := httptest.NewServer(srv.Handler())
	return srv, hub, ts
}

func TestHealth(t *testing.T) {
	_, _, ts := newTestServer()
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
	_, _, ts := newTestServer()
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
	_, _, ts := newTestServer()
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

func TestWebSocketUpgrade(t *testing.T) {
	_, hub, ts := newTestServer()
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Wait for registration.
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() < 1 {
		t.Fatalf("client not registered, count = %d", hub.ClientCount())
	}
}

func TestUpdateSDRInvalidJSON(t *testing.T) {
	_, _, ts := newTestServer()
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