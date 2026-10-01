package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"sigint-workbench/internal/ws"
)

// testServer starts an in-process ws-hub with the ingest and WS routes.
func testServer(t *testing.T) (*httptest.Server, *ws.Hub) {
	t.Helper()
	hub := ws.NewHub()
	go hub.Run()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", handleWS(hub))
	mux.HandleFunc("/api/events", handleIngest(hub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, hub
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