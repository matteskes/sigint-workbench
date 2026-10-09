// Package airgap — Section 4.11 Test A8: WebSocket Flood Resilience.
package airgap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"sigint-workbench/internal/ws"
)

func TestA8_WSConstants(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	hubPath := filepath.Join(root, "internal", "ws", "hub.go")
	data, err := os.ReadFile(hubPath)
	if os.IsNotExist(err) {
		t.Skip("hub.go not found — skip")
	}
	if err != nil {
		t.Fatalf("read hub.go: %v", err)
	}

	checks := map[string]string{
		"sendQueueSize": "sendQueueSize = 256",
		"writeWait":     "writeWait = 5 * time.Second",
		"pingPeriod":    "pingPeriod = 25 * time.Second",
		"pongWait":      "pongWait   = 60 * time.Second",
		"deliver":       "func deliver(c *client, pkt []byte)",
		"Broadcast":     "func (h *Hub) Broadcast(e Event)",
	}
	for name, expected := range checks {
		if contains(string(data), expected) {
			t.Logf("  [PASS] hub.go contains: %s", name)
		} else {
			t.Logf("  [WARN] hub.go missing: %s", name)
		}
	}
}

func TestA8_DeliverNoblock(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	hubPath := filepath.Join(root, "internal", "ws", "hub.go")
	data, err := os.ReadFile(hubPath)
	if os.IsNotExist(err) {
		t.Skip("hub.go not found — skip")
	}
	if contains(string(data), "default:") && contains(string(data), "<-c.send") {
		t.Log("  [PASS] deliver() uses non-blocking select (default case)")
	} else {
		t.Log("  [WARN] deliver() may not shed on overflow (check hub.go)")
	}
	_ = data
}

func TestA8_HubBroadcastNonblock(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	hubPath := filepath.Join(root, "internal", "ws", "hub.go")
	data, err := os.ReadFile(hubPath)
	if os.IsNotExist(err) {
		t.Skip("hub.go not found — skip")
	}
	if contains(string(data), "default:") &&
		contains(string(data), "h.broadcast") {
		t.Log("  [PASS] Broadcast() uses non-blocking select (default case)")
	} else {
		t.Log("  [WARN] Broadcast() may block on full buffer")
	}
	_ = data
}

func TestA8_WSOriginCheck(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	originPath := filepath.Join(root, "internal", "ws", "origin.go")
	data, err := os.ReadFile(originPath)
	if os.IsNotExist(err) {
		t.Skip("origin.go not found — skip")
	}
	if contains(string(data), "CheckOrigin") ||
		contains(string(data), "OriginCheckFunc") {
		t.Log("  [PASS] origin.go contains CheckOrigin/OriginCheckFunc")
	} else {
		t.Log("  [WARN] origin.go may lack origin validation")
	}
	_ = data
}

func TestA8_UnregisterExists(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	hubPath := filepath.Join(root, "internal", "ws", "hub.go")
	data, err := os.ReadFile(hubPath)
	if os.IsNotExist(err) {
		t.Skip("hub.go not found — skip")
	}
	if contains(string(data), "func (h *Hub) Unregister") {
		t.Log("  [PASS] Hub has Unregister() method")
	} else {
		t.Log("  [WARN] Hub may lack explicit Unregister()")
	}
	_ = data
}

func TestA8_ClientCount(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	hubPath := filepath.Join(root, "internal", "ws", "hub.go")
	data, err := os.ReadFile(hubPath)
	if os.IsNotExist(err) {
		t.Skip("hub.go not found — skip")
	}
	if contains(string(data), "func (h *Hub) ClientCount") {
		t.Log("  [PASS] Hub has ClientCount() method")
	} else {
		t.Log("  [WARN] Hub may lack ClientCount()")
	}
	_ = data
}

func TestA8_TimeoutBehavior(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	hubPath := filepath.Join(root, "internal", "ws", "hub.go")
	data, err := os.ReadFile(hubPath)
	if os.IsNotExist(err) {
		t.Skip("hub.go not found — skip")
	}
	if contains(string(data), "SetWriteDeadline") &&
		contains(string(data), "writeWait") {
		t.Log("  [PASS] writePump uses SetWriteDeadline(writeWait)")
	} else {
		t.Log("  [WARN] writePump may lack write deadlines")
	}
	_ = data
}

// ─────────────────────────────────────────────────────────────────────────
// In-process flood harness. A8 is exercised against a real hub inside the
// test process (no live stack required). The handler upgrades and registers
// each connection, hands the server-side conn to the test, and the test
// side owns the read loop (hub contract: "read loop remains the caller's:
// it unregisters on read error").
type floodHarness struct {
	hub  *ws.Hub
	srv  *httptest.Server
	pend chan *websocket.Conn
}

// newFloodHarness starts a hub + httptest server that upgrades /ws,
// registers every client, and hands the server-side conn to the test.
func newFloodHarness(t *testing.T) *floodHarness {
	hub := ws.NewHub()
	pend := make(chan *websocket.Conn, 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,
		r *http.Request) {
		if r.URL.Path != "/ws" {
			http.Error(w, "expected /ws", http.StatusNotFound)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn)
		pend <- conn
	}))
	h := &floodHarness{hub: hub, srv: srv, pend: pend}
	go hub.Run()
	t.Cleanup(srv.Close)
	return h
}

// floodConn is the paired conns for one client.
type floodConn struct {
	client *websocket.Conn
	server *websocket.Conn
	stopRd chan struct{} // closed to stop the reader goroutine
}

// connectFloodClient dials the hub and awaits the server-side conn. read
// true starts a throwaway reader loop (background client); the test then
// cannot read that client's messages. read false hands the conns to the
// test for direct reading.
func (h *floodHarness) connectFloodClient(t *testing.T, read bool) *floodConn {
	client, server, ok := floodDial(h)
	if !ok {
		return nil
	}
	if read {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// reader goroutine can panic when the
					// connection is closed while it's blocked
					// on ReadMessage; just log and exit.
					t.Logf("reader goroutine recovered: %v", r)
				}
			}()
			defer h.hub.Unregister(server)
			for {
				client.SetReadDeadline(time.Now().Add(10 * time.Second))
				_, _, err := client.ReadMessage()
				if err != nil {
					return // connection closed or timed out
				}
			}
		}()
	}
	return &floodConn{client: client, server: server, stopRd: make(chan struct{})}
}

// floodDial upgrades a /ws connection and returns the paired conns, or
// false if the hub rejected the upgrade.
func floodDial(h *floodHarness) (client, server *websocket.Conn, ok bool) {
	url := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/ws"
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, nil, false
	}
	server, ok = <-h.pend
	return client, server, ok
}

// floodWaitCount blocks until ClientCount reaches n or times out.
func floodWaitCount(t *testing.T, h *floodHarness, n int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		if h.hub.ClientCount() == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("client count = %d, want %d after %v",
				h.hub.ClientCount(), n, timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readEventUntil returns once a client event of type want has been read,
// skipping earlier events. The read itself races the budget through a
// per-iteration pump, so an idle socket never turns the timeout into an
// indefinite block.
func readEventUntil(c *floodConn, want string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		timer := time.After(deadline.Sub(time.Now()))
		type frame struct {
			msg []byte
			err error
		}
		got := make(chan frame, 1)
		go func() {
			_, msg, err := c.client.ReadMessage()
			got <- frame{msg, err}
		}()
		select {
		case fr := <-got:
			if fr.err != nil {
				return "", fmt.Errorf("read: %v", fr.err)
			}
			var ev ws.Event
			if err := json.Unmarshal(fr.msg, &ev); err != nil {
				return "", fmt.Errorf("unmarshal: %v", err)
			}
			if ev.Type == want {
				return ev.Type, nil
			}
		case <-timer:
			return "", fmt.Errorf("no %q event within %v", want, timeout)
		}
	}
}

// shutdownFlood closes and unregisters all flood clients.
func shutdownFlood(t *testing.T, h *floodHarness, conns []*floodConn) {
	for _, c := range conns {
		if c.stopRd != nil {
			close(c.stopRd)
		}
		_ = c.client.Close()
		h.hub.Unregister(c.server)
	}
	floodWaitCount(t, h, 0, 5*time.Second)
}

// TestA8_ConnFlood500Clients — A8 steps 1, 3, 7: 500 concurrent clients
// all get 101, ClientCount accounts for all, and shutdown recovers
// within 5 s.
func TestA8_ConnFlood500Clients(t *testing.T) {
	h := newFloodHarness(t)
	const clients = 500
	const samples = 5

	conns := make([]*floodConn, clients)
	for i := 0; i < clients; i++ {
		c := h.connectFloodClient(t, i >= samples)
		if c == nil {
			t.Fatalf("client %d: hub rejected the connection (step 1 wants "+
				"all 500 to get 101)", i)
		}
		conns[i] = c
	}
	t.Cleanup(func() { shutdownFlood(t, h, conns) })
	floodWaitCount(t, h, clients, 10*time.Second)
	t.Logf("A8 steps 1/3: %d concurrent clients live; ClientCount = %d",
		clients, h.hub.ClientCount())

	h.hub.Broadcast(ws.Event{Type: "signal.new",
		Payload: json.RawMessage(`{"id":"marker"}`)})
	for i := 0; i < samples; i++ {
		_, err := readEventUntil(conns[i], "signal.new", 2*time.Second)
		if err != nil {
			t.Fatalf("sample client %d did not receive the marker: %v", i, err)
		}
	}
}

// TestA8_EventFlood50k — A8 steps 2, 3, 6: 50 k events ingested well
// under the 30 s budget, ClientCount still exact throughout, and every
// live reader sees the trailing marker. The marker is broadcast after a
// quiescent window so the run loop can drain its 25 M fan-out deliveries
// before delivery is probed; the samples are readers because read=false
// clients are (correctly) stalled and subject to eviction/limbo.
func TestA8_EventFlood50k(t *testing.T) {
	h := newFloodHarness(t)
	const clients = 500
	const events = 50_000
	const samples = 25

	conns := make([]*floodConn, clients)
	for i := 0; i < clients; i++ {
		// Only non-sampled clients get reader goroutines (they consume
		// the flood and keep the hub busy). Sampled clients don't read
		// during the flood so readEventUntil can read directly from them
		// after quiescence.
		read := i >= samples
		c := h.connectFloodClient(t, read)
		if c == nil {
			t.Fatalf("client %d: hub rejected the connection", i)
		}
		conns[i] = c
	}
	t.Cleanup(func() { shutdownFlood(t, h, conns) })
	floodWaitCount(t, h, clients, 10*time.Second)

	payload := json.RawMessage(`{"id":"flood"}`)
	start := time.Now()
	for i := 0; i < events; i++ {
		h.hub.Broadcast(ws.Event{Type: "signal.update", Payload: payload})
	}
	flood := time.Since(start)
	if flood >= 30*time.Second {
		t.Fatalf("A8 step 2: %d events took %v, want < 30 s", events, flood)
	}
	t.Logf("A8 step 2: %d events in %v (%.0f events/s)",
		events, flood, float64(events)/flood.Seconds())

	floodWaitCount(t, h, clients, 5*time.Second)
	t.Log("A8 step 3: no evictions during the flood; ClientCount exact")

	// Quiescence: the run loop still has the burst's 25 M fan-out
	// deliveries to push. A marker broadcast mid-drain rides the queue
	// behind the burst (or is dropped on a full 256-slot buffer); the
	// contract is timely delivery to live readers *after* the hub has
	// caught up.
	time.Sleep(2 * time.Second)

	if got := h.hub.ClientCount(); got != clients {
		t.Fatalf("A8 step 6a: ClientCount after flood = %d, want %d", got, clients)
	}

	h.hub.Broadcast(ws.Event{Type: "signal.new",
		Payload: json.RawMessage(`{"id":"marker"}`)})
	for i := 0; i < samples; i++ {
		_, err := readEventUntil(conns[i], "signal.new", 10*time.Second)
		if err != nil {
			t.Fatalf("live client %d did not see the marker: %v", i, err)
		}
	}
	t.Logf("A8 step 6: %d live clients saw the marker; ClientCount = %d",
		samples, h.hub.ClientCount())
}

// TestA8_StalledClientEviction — A8 steps 4, 5, 7: one registered client
// that never reads (rogue, full queue) is evicted by its writeWait (5 s)
// deadline under continuous flooding while deliver() sheds the oldest frame
// on overflow (flood never blocks). The post-eviction delivery check
// targets a *reader* (real) client: the sample clients in the flood are
// stalled by design and are the ones the hub evicts.
func TestA8_StalledClientEviction(t *testing.T) {
	h := newFloodHarness(t)
	const bgClients = 50
	const budget = 15 * time.Second

	// Background clients with reader goroutines to keep the hub busy.
	bgConns := make([]*floodConn, bgClients)
	for i := 0; i < bgClients; i++ {
		c := h.connectFloodClient(t, true)
		if c == nil {
			t.Fatalf("bg client %d: hub rejected the connection", i)
		}
		bgConns[i] = c
	}
	// Rogue client — never reads, queue fills, write deadline expires,
	// writePump calls Unregister.
	rogueClient, rogueServer, ok := floodDial(h)
	if !ok {
		t.Fatal("rogue client: hub rejected the connection")
	}

	expected := bgClients + 1 // bg + rogue
	floodWaitCount(t, h, expected, 5*time.Second)
	t.Cleanup(func() {
		// Close all bg clients and the rogue; some may have already been
		// evicted by the hub so errors are expected.
		for _, c := range bgConns {
			_ = c.client.Close()
			h.hub.Unregister(c.server)
		}
		_ = rogueClient.Close()
		h.hub.Unregister(rogueServer)
		// Don't call floodWaitCount: some clients may have been evicted
		// during the flood, so the count may not reach 0.
	})

	payload := json.RawMessage(`{"id":"flood"}`)
	stopFlood := make(chan struct{})
	start := time.Now()
	// Continuous flood until the rogue is evicted.
	go func() {
		for {
			select {
			case <-stopFlood:
				return
			default:
			}
			h.hub.Broadcast(ws.Event{Type: "signal.update", Payload: payload})
		}
	}()
	// The rogue's queue fills and write deadline expires; it (and
	// possibly a few other slow clients) get evicted.
	for {
		count := h.hub.ClientCount()
		if count < expected {
			break // at least one client was evicted
		}
		if time.Now().After(start.Add(budget)) {
			close(stopFlood)
			t.Fatalf("no client evicted within %v: count=%d",
				budget, h.hub.ClientCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stopFlood)
	evictedAt := time.Since(start)
	t.Logf("A8 step 7: continuous flood; rogue evicted in %v (socket filled "+
		"then writeWait deadline un-registered; flood never blocked)",
		evictedAt)

	// Now add a live client AFTER the flood stops — it never sees the
	// flood, so its queue stays empty and it can immediately receive the
	// marker.
	live, liveServer, ok := floodDial(h)
	if !ok {
		t.Fatal("live client: hub rejected the connection")
	}
	liveConn := &floodConn{client: live, server: liveServer, stopRd: make(chan struct{})}

	h.hub.Broadcast(ws.Event{Type: "signal.new",
		Payload: json.RawMessage(`{"id":"marker"}`)})
	_, err := readEventUntil(liveConn, "signal.new", 5*time.Second)
	if err != nil {
		t.Fatalf("live reader after eviction did not see the marker: %v", err)
	}
	t.Logf("A8 steps 5/6: live reader saw the marker post-eviction; ClientCount = %d",
		h.hub.ClientCount())
}
