//go:build !onnx

package airgap

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"sigint-workbench/internal/ws"
)

// TestProbe4_MarkerTiming isolates the 50k-flood marker path with 500
// reader clients.
//
// Diagnostic probe, not a regression test — it broadcasts 50k events,
// dumps all goroutine stacks, and prints per-client marker latency. It
// cannot fit in the 10-minute `go test` package timeout under parallel
// package load (it panicked inside `make test`). Run it on demand with
// SIGINT_FLOOD_PROBE=1.
func TestProbe4_MarkerTiming(t *testing.T) {
	if os.Getenv("SIGINT_FLOOD_PROBE") == "" {
		t.Skip("diagnostic probe; set SIGINT_FLOOD_PROBE=1 to run")
	}
	h := newFloodHarness(t)
	const clients = 500
	conns := make([]*floodConn, clients)
	for i := 0; i < clients; i++ {
		c := h.connectFloodClient(t, true)
		if c == nil {
			t.Fatalf("client %d: rejected", i)
		}
		conns[i] = c
	}
	t.Cleanup(func() { shutdownFlood(t, h, conns) })
	floodWaitCount(t, h, clients, 10*time.Second)

	payload := json.RawMessage(`{"id":"flood"}`)
	t0 := time.Now()
	for i := 0; i < 50_000; i++ {
		h.hub.Broadcast(ws.Event{Type: "signal.update", Payload: payload})
	}
	fmt.Printf("PROBE4: flood done in %v, ClientCount=%d\n", time.Since(t0), h.hub.ClientCount())
	time.Sleep(1500 * time.Millisecond)
	// Dump stacks to see what the run loop is doing when the marker
	// is broadcast. Truncate to the hub-relevant frames.
	space := make([]byte, 5*1024*1024)
	n := runtime.Stack(space, true)
	fmt.Printf("PROBE4-stack (%d bytes):\n%s\n", n, string(space[:n]))
	fmt.Printf("PROBE4: quiescent, ClientCount=%d\n", h.hub.ClientCount())
	h.hub.Broadcast(ws.Event{Type: "signal.new",
		Payload: json.RawMessage(`{"id":"marker"}`)})
	start := time.Since(t0)
	for _, idx := range []int{0, 100, 200, 300, 400} {
		msg, err := readEventUntil(conns[idx], "signal.new", 8*time.Second)
		fmt.Printf("PROBE4: client %d: t=%+v result=%q err=%v\n",
			idx, time.Since(t0), msg, err)
	}
	fmt.Printf("PROBE4: final ClientCount=%d\n", h.hub.ClientCount())
	_ = start
}
