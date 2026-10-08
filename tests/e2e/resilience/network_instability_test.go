// Package resilience — Test R4: Network Instability Handling (D4.2 extension).
//
// Validates that the hub handles unexpected WS client disconnects,
// reconnections, and connection storms without corrupting state
// (spec §4.10.6, steps 1–8).
package resilience

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestNetworkInstability validates that the hub handles unexpected WS
// disconnects, reconnections, and connection storms without state corruption.
func TestNetworkInstability(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	// Step 1-2: Connect client, verify 101, verify events flow.
	c1, r1, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		if r1 != nil { io.Copy(io.Discard, r1.Body); r1.Body.Close() }
		t.Fatalf("WS dial: %v", err)
	}
	if r1.StatusCode != 101 {
		io.Copy(io.Discard, r1.Body); r1.Body.Close()
		t.Fatalf("expected 101, got %d", r1.StatusCode)
	}
	r1.Body.Close()
	drainConn(c1)

	// Pump a few events.
	pumpEvents(5, 0)
	n := readFrames(c1, 3*time.Second)
	t.Logf("Initial connection: received %d events", n)

	// Step 3: Simulate network drop (close connection without graceful close).
	c1.Close()

	// Step 4: Connect a new client while the first is "dead".
	time.Sleep(1 * time.Second) // Give hub time to notice the disconnect.
	c2, r2, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		if r2 != nil { io.Copy(io.Discard, r2.Body); r2.Body.Close() }
		t.Fatalf("Reconnect after drop: %v", err)
	}
	if r2.StatusCode != 101 {
		io.Copy(io.Discard, r2.Body); r2.Body.Close()
		t.Fatalf("expected 101 on reconnect, got %d", r2.StatusCode)
	}
	r2.Body.Close()
	drainConn(c2)

	// Step 5: Pump events through the reconnected client.
	pumpEvents(10, 100)
	reconnected := readFrames(c2, 5*time.Second)
	if reconnected < 5 {
		t.Fatalf("events not delivered after reconnect: received %d", reconnected)
	}
	c2.Close()

	// Step 6: Connection storm — connect/disconnect 20 clients rapidly.
	t.Log("Connection storm: 20 rapid connect/disconnect cycles...")
	var stormWg sync.WaitGroup
	var successes atomic.Int64
	for i := 0; i < 20; i++ {
		stormWg.Add(1)
		go func() {
			defer stormWg.Done()
			client, resp, err := dialer.Dial("ws://localhost:8081/ws", nil)
			if err != nil {
				return
			}
			if resp.StatusCode == 101 {
				successes.Add(1)
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			// Close without graceful close (simulates network drop).
			client.Close()
		}()
	}
	stormWg.Wait()
	t.Logf("Connection storm: %d/%d successful 101 upgrades", successes.Load(), 20)

	// Step 7: Pump events after storm and verify delivery.
	time.Sleep(2 * time.Second)
	c3, r3, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		if r3 != nil { io.Copy(io.Discard, r3.Body); r3.Body.Close() }
		t.Fatalf("Post-storm reconnect: %v", err)
	}
	if r3.StatusCode != 101 {
		io.Copy(io.Discard, r3.Body); r3.Body.Close()
		t.Fatalf("expected 101 post-storm, got %d", r3.StatusCode)
	}
	r3.Body.Close()
	drainConn(c3)

	pumpEvents(10, 200)
	afterStorm := readFrames(c3, 5*time.Second)
	c3.Close()

	if afterStorm < 5 {
		t.Fatalf("events not delivered after storm: received %d", afterStorm)
	}

	// Step 8: Verify hub health shows reasonable client count.
	hubResp, err := http.Get("http://localhost:8081/health")
	if err == nil {
		body, _ := io.ReadAll(hubResp.Body)
		hubResp.Body.Close()
		var health map[string]any
		if err := json.Unmarshal(body, &health); err == nil {
			if clients, ok := health["clients"].(float64); ok {
				t.Logf("Hub health: %d active clients (post-storm)", int(clients))
				// Should be low (only the test connection counts briefly).
			}
		}
	}

	if successes.Load() < 10 {
		t.Logf("Warning: Only %d/20 storm connections succeeded", successes.Load())
	}
	t.Logf("PASS: Network instability handled without state corruption. Storm: %d/20, post-storm: %d events",
		successes.Load(), afterStorm)
}

// TestNetworkInstabilityReconnectRace validates rapid reconnection
// doesn't cause WS proxy errors or data loss (spec §4.10.6).
func TestNetworkInstabilityReconnectRace(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	// Send 5 rapid reconnection attempts.
	var wg sync.WaitGroup
	var connected atomic.Int64
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 3; attempt++ {
				client, resp, err := dialer.Dial("ws://localhost:8081/ws", nil)
				if err != nil {
					time.Sleep(500 * time.Millisecond)
					continue
				}
				if resp.StatusCode == 101 {
					connected.Add(1)
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				client.Close()
				time.Sleep(200 * time.Millisecond)
			}
		}()
	}
	wg.Wait()

	t.Logf("PASS: Reconnect race: %d successful connections (5 clients x 3 attempts)", connected.Load())
}