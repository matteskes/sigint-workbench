package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"sigint-workbench/internal/ws"
)

// testServer starts an in-process ws-hub with the ingest and WS routes.
// The admission cap is disabled (nil gate) — capped variants use
// testServerCapped.
func testServer(t *testing.T) (*httptest.Server, *ws.Hub) {
	t.Helper()
	return testServerCapped(t, 0)
}

// testServerCapped starts an in-process ws-hub whose /ws upgrades are
// bounded by a ClientLimiter of max clients (0 = unlimited).
func testServerCapped(t *testing.T, max int) (*httptest.Server, *ws.Hub) {
	t.Helper()
	hub := ws.NewHub()
	gate := ws.NewClientLimiter(max)
	go hub.Run()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", handleWS(hub, gate))
	mux.HandleFunc("/api/events", handleIngest(hub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, hub
}

// waitForClientsBlocked polls until the hub reports exactly n clients.
func waitForClientsBlocked(t *testing.T, hub *ws.Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() != n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() != n {
		t.Fatalf("expected %d client(s), got %d", n, hub.ClientCount())
	}
}

// waitForClients blocks until the hub reports n connected clients
// or the timeout expires.
func waitForClients(t *testing.T, hub *ws.Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() < n {
		t.Fatalf("expected %d client(s), got %d", n, hub.ClientCount())
	}
}

func TestIngestBroadcastsToClients(t *testing.T) {
	srv, hub := testServer(t)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitForClients(t, hub, 1)

	payload := map[string]interface{}{"id": "abc", "freqHz": 146520000}
	body := `{"type":"signal.new","payload":` + mustJSON(t, payload) + `}`
	resp, err := http.Post(srv.URL+"/api/events", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ev ws.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if ev.Type != "signal.new" {
		t.Fatalf("event type = %q, want signal.new", ev.Type)
	}
	if !bytes.Equal(ev.Payload, mustJSONBytes(t, payload)) {
		t.Fatalf("payload = %s, want %s", ev.Payload, payload)
	}
}

func TestIngestBroadcastsSpectrumFrame(t *testing.T) {
	// §18: the hub must relay spectrum.frame display feed events.
	srv, hub := testServer(t)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	waitForClients(t, hub, 1)

	payload := map[string]interface{}{
		"sdrId": "sim0", "freqHz": 100000000, "sampleRate": 2400000,
		"t": "2026-10-04T12:00:00Z", "bins": 4, "df": 585.9375,
		"db": []float64{-87.3, -84.1, -90.0, -88.5},
	}
	body := `{"type":"spectrum.frame","payload":` + mustJSON(t, payload) + `}`
	resp, err := http.Post(srv.URL+"/api/events", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ev ws.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if ev.Type != "spectrum.frame" {
		t.Fatalf("event type = %q, want spectrum.frame", ev.Type)
	}
	if !bytes.Equal(ev.Payload, mustJSONBytes(t, payload)) {
		t.Fatalf("payload = %s, want %s", ev.Payload, payload)
	}
}

func TestIngestRejectsUnknownType(t *testing.T) {
	srv, _ := testServer(t)
	resp, err := http.Post(srv.URL+"/api/events", "application/json",
		strings.NewReader(`{"type":"bogus","payload":{}}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestIngestRejectsInvalidJSON(t *testing.T) {
	srv, _ := testServer(t)
	resp, err := http.Post(srv.URL+"/api/events", "application/json",
		strings.NewReader(`{"type":`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestIngestRejectsNonPost(t *testing.T) {
	srv, _ := testServer(t)
	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

// TestWSClientCapEnforced: upgrades beyond WS_HUB_MAX_CLIENTS are
// refused with 503 + D10 body before the handshake, and a released
// slot is immediately reusable (§14.3, A8 follow-up).
func TestWSClientCapEnforced(t *testing.T) {
	srv, hub := testServerCapped(t, 1)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	first, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer first.Close()
	waitForClients(t, hub, 1)

	_, resp2, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("second dial must be refused at capacity")
	}
	if resp2 == nil {
		t.Fatalf("refusal must carry an HTTP response: %v", err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", resp2.StatusCode, body)
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil || m["error"] == nil {
		t.Fatalf("body = %s, want D10 {\"error\":...}", body)
	}
	if hub.ClientCount() != 1 {
		t.Fatalf("rejected client must not register: count = %d", hub.ClientCount())
	}

	// Free the slot; the read loop's exit releases it and the next
	// upgrade succeeds again.
	first.Close()
	waitForClientsBlocked(t, hub, 0)
	third, resp3, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial after release: %v", err)
	}
	third.Close()
	if resp3.StatusCode != http.StatusSwitchingProtocols {
		io.Copy(io.Discard, resp3.Body)
		t.Fatalf("status = %d, want 101", resp3.StatusCode)
	}
	resp3.Body.Close()
}

// TestMaxClientsFromEnv covers WS_HUB_MAX_CLIENTS parsing: unset →
// default, "0" → unlimited, valid → as set, negative/invalid →
// default (fail-safe).
func TestMaxClientsFromEnv(t *testing.T) {
	orig := envLookup
	defer func() { envLookup = orig }()
	cases := []struct {
		raw  string
		set  bool
		want int
	}{
		{"", false, defaultMaxClients}, // unset
		{"", true, defaultMaxClients},  // set-but-empty
		{"0", true, 0},                 // explicit unlimited
		{"5", true, 5},
		{"2048", true, 2048},
		{"-3", true, defaultMaxClients},  // negative → fail-safe default
		{"abc", true, defaultMaxClients}, // invalid → fail-safe default
	}
	for _, tc := range cases {
		raw, set := tc.raw, tc.set
		envLookup = func(string) (string, bool) { return raw, set }
		if got := MaxClientsFromEnv(); got != tc.want {
			t.Errorf("WS_HUB_MAX_CLIENTS=%q (set=%v): got %d, want %d",
				raw, set, got, tc.want)
		}
	}
}

func mustJSON(t *testing.T, v interface{}) string {

	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func mustJSONBytes(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
