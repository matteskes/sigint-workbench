package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestHub starts a hub with its Run loop in a goroutine behind a
// test HTTP server that upgrades and registers WebSocket clients.
func newTestHub(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	hub := NewHub()
	go hub.Run()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn)
		go func() {
			defer hub.Unregister(conn)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
	}))
	t.Cleanup(srv.Close)
	return hub, srv
}

func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func waitClientCount(t *testing.T, hub *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() != n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := hub.ClientCount(); got != n {
		t.Fatalf("client count = %d, want %d", got, n)
	}
}

func TestHub_RegisterAndUnregister(t *testing.T) {
	hub, srv := newTestHub(t)
	c1 := dialWS(t, srv)
	c2 := dialWS(t, srv)
	waitClientCount(t, hub, 2)

	// Closing the client ends the handler's read loop, whose deferred
	// Unregister(serverConn) mirrors the production disconnect path.
	// (The hub tracks server-side conns, so the test must not call
	// hub.Unregister with the client-side conn.)
	c1.Close()
	waitClientCount(t, hub, 1)
	_ = c2
}

func TestHub_BroadcastsEventToAllClients(t *testing.T) {
	hub, srv := newTestHub(t)
	c1 := dialWS(t, srv)
	c2 := dialWS(t, srv)
	waitClientCount(t, hub, 2)

	hub.Broadcast(Event{Type: "signal.new", Payload: json.RawMessage(`{"id":"sig-1"}`)})

	clients := map[string]*websocket.Conn{"c1": c1, "c2": c2}
	for name, conn := range clients {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("%s read: %v", name, err)
		}
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("%s unmarshal: %v", name, err)
		}
		if ev.Type != "signal.new" {
			t.Fatalf("%s event type = %q, want signal.new", name, ev.Type)
		}
		if string(ev.Payload) != `{"id":"sig-1"}` {
			t.Fatalf("%s payload = %s", name, ev.Payload)
		}
	}
}

func TestHub_BroadcastDropsWhenBufferFull(t *testing.T) {
	// Without Run() draining the channel, Broadcast must stay
	// non-blocking once the 256-slot buffer is full.
	hub := NewHub()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1024; i++ {
			hub.Broadcast(Event{Type: "fill", Payload: json.RawMessage(`{}`)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked when buffer full; expected non-blocking drop")
	}
}

func TestEvent_JSONShape(t *testing.T) {
	b, err := json.Marshal(Event{Type: "sdr.status", Payload: json.RawMessage(`{"id":"a"}`)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"type", "payload"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing %q key in %s", key, b)
		}
	}
	var ev Event
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatalf("unmarshal into Event: %v", err)
	}
	if ev.Type != "sdr.status" {
		t.Fatalf("Type = %q, want sdr.status", ev.Type)
	}
	if string(ev.Payload) != `{"id":"a"}` {
		t.Fatalf("Payload = %s", ev.Payload)
	}
}

// TestHub_DeliverShedsOldest pins the §14.3 shed-oldest contract of
// the per-client queue: overflow evicts the OLDEST frame (newest
// state wins for last-wins consumers like signal.update), and deliver
// never blocks even when nobody drains.
func TestHub_DeliverShedsOldest(t *testing.T) {
	c := &client{send: make(chan []byte, 2)}
	deliver(c, []byte("a"))
	deliver(c, []byte("b"))
	// Queue full: the next push sheds "a", keeps the newest two.
	deliver(c, []byte("c"))
	if got := string(<-c.send); got != "b" {
		t.Fatalf("first frame after shed = %q, want %q", got, "b")
	}
	if got := string(<-c.send); got != "c" {
		t.Fatalf("second frame after shed = %q, want %q", got, "c")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			deliver(c, []byte("x"))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deliver blocked on a full queue; expected shed-oldest")
	}
}

// TestHub_SlowClientDoesNotStallHealthyClients is the A1 regression
// test: a client that stops reading must not delay delivery to other
// clients (the old hub wrote synchronously inside the broadcast loop).
func TestHub_SlowClientDoesNotStallHealthyClients(t *testing.T) {
	hub, srv := newTestHub(t)
	healthy := dialWS(t, srv)
	waitClientCount(t, hub, 1)

	// A second client that never reads: its server-side queue (and
	// kernel buffers) fill while the healthy client must keep flowing.
	_ = dialWS(t, srv)
	waitClientCount(t, hub, 2)

	for i := 0; i < sendQueueSize*4; i++ {
		hub.Broadcast(Event{Type: "signal.update", Payload: json.RawMessage(`{"id":"x"}`)})
	}
	healthy.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := healthy.ReadMessage(); err != nil {
		t.Fatalf("healthy client starved by slow peer: %v", err)
	}
}

// TestHub_SilentClientIsEvicted is the A12 regression test: a client
// that never answers pings is dropped once the read deadline lapses
// (zoomed-out constants keep the test fast; production uses 25s/60s).
func TestHub_SilentClientIsEvicted(t *testing.T) {
	hub := NewHub()
	hub.pingPeriod = 100 * time.Millisecond
	hub.pongWait = 250 * time.Millisecond
	go hub.Run()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn)
		go func() {
			defer hub.Unregister(conn)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
	}))
	t.Cleanup(srv.Close)

	dialWS(t, srv)
	waitClientCount(t, hub, 1)

	// The client never reads (so never pongs). Its server-side read
	// deadline lapses, the handler's read loop exits, and the hub
	// drops the zombie instead of accumulating dead peers.
	deadline := time.Now().Add(3 * time.Second)
	for hub.ClientCount() == 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := hub.ClientCount(); got != 0 {
		t.Fatalf("silent client still connected after pongWait; count = %d", got)
	}
}
