//go:build !onnx

package airgap

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"sigint-workbench/internal/ws"
)

// TestProbe3_SampleLifecycle isolates the sample-client dynamics of the
// eviction test: 46 draining readers, 5 stalled samples, 1 rogue. The flood
// runs until the rogue is evicted; then the marker goes out and each sample
// is read individually with a full timestamp.
//
// Diagnostic probe, not a regression test — it prints timings and stack
// dumps and blocks for minutes under parallel package load (it blew the
// 10-minute `go test` package timeout inside `make test`). Run it on
// demand with SIGINT_FLOOD_PROBE=1.
func TestProbe3_SampleLifecycle(t *testing.T) {
	if os.Getenv("SIGINT_FLOOD_PROBE") == "" {
		t.Skip("diagnostic probe; set SIGINT_FLOOD_PROBE=1 to run")
	}
	h := newFloodHarness(t)
	const fast = 46
	const samples = 5

	conns := make([]*floodConn, fast+samples+1)
	for i := 0; i < fast+samples; i++ {
		c := h.connectFloodClient(t, i >= samples)
		if c == nil {
			t.Fatalf("client %d: rejected", i)
		}
		conns[i] = c
	}
	rogueClient, rogueServer, ok := floodDial(h)
	if !ok {
		t.Fatal("rogue: rejected")
	}
	conns[fast+samples] = &floodConn{client: rogueClient, server: rogueServer}

	floodWaitCount(t, h, fast+samples+1, 10*time.Second)
	fmt.Printf("PROBE3: t=0.000 ClientCount=%d (want %d)\n",
		h.hub.ClientCount(), fast+samples+1)

	ticker := time.NewTicker(500 * time.Millisecond)
	go func() {
		t0 := time.Now()
		n := 0
		for range ticker.C {
			if n > 30 {
				break
			}
			fmt.Printf("PROBE3: t=%+v ClientCount=%d\n",
				time.Since(t0), h.hub.ClientCount())
			n++
		}
	}()
	defer ticker.Stop()

	payload := json.RawMessage(`{"id":"flood"}`)
	stopFlood := make(chan struct{})
	start := time.Now()
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
	rogueEvicted := false
	for !rogueEvicted {
		if h.hub.ClientCount() == fast+samples {
			rogueEvicted = true
		}
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Printf("PROBE3: rogue evicted at t=%+v (ClientCount=%d)\n",
		time.Since(start), h.hub.ClientCount())
	close(stopFlood)

	h.hub.Broadcast(ws.Event{Type: "signal.new",
		Payload: json.RawMessage(`{"id":"marker"}`)})
	fmt.Println("PROBE3: marker broadcast, reading samples...")
	for i := 0; i < samples; i++ {
		msg, err := readEventUntil(conns[i], "signal.new", 4*time.Second)
		fmt.Printf("PROBE3: sample %d: t=%+v result=%q err=%v\n",
			i, time.Since(start), msg, err)
	}
}
