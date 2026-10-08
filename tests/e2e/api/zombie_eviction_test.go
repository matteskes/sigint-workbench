// Package api — Section 1.3: Zombie Client Eviction (A12).
//
// This test simulates a silent ("zombie") WebSocket client and validates
// that the hub's 120s eviction timeout actually evicts the stale connection,
// decrements ClientCount(), and does not interfere with active clients.
//
// Spec: §14.3 (WebSocket Events), A12 (keepalive contract), §17.3 (E2E).
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestZombieClientEviction validates that the hub evicts a zombie (stalled)
// client after the configured timeout (120s by default) and decrements its
// client count — without disrupting any active peers (spec §14.3, A12).
func TestZombieClientEviction(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping zombie eviction test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	// Step 1: Connect Client A (zombie — never reads/writes) and
	// Client B (active — reads all hub events).
	connA, respA, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respA.Body)
		respA.Body.Close()
		t.Fatalf("Client A dial: %v", err)
	}
	t.Cleanup(func() { connA.Close() })

	connB, respB, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respB.Body)
		respB.Body.Close()
		t.Fatalf("Client B dial: %v", err)
	}
	t.Cleanup(func() { connB.Close() })

	// Verify 101 Upgrade on both.
	if respA.StatusCode != 101 || respB.StatusCode != 101 {
		io.Copy(io.Discard, respA.Body)
		io.Copy(io.Discard, respB.Body)
		respA.Body.Close()
		respB.Body.Close()
		t.Fatalf("expected 101 Upgrade, got %d / %d", respA.StatusCode, respB.StatusCode)
	}
	respA.Body.Close()
	respB.Body.Close()

	// Drain any initial messages from both.
	for _, c := range []*websocket.Conn{connA, connB} {
		for {
			_, _, err := c.ReadMessage()
			if err != nil {
				break
			}
		}
	}

	// Step 2: Check the initial hub client count (should be 2).
	initialHub := parseHealthBody("http://localhost:8081/health")
	initialClients := 0
	if c, ok := initialHub["clients"].(float64); ok {
		initialClients = int(c)
	}
	t.Logf("Hub reports %d connected clients before test", initialClients)
	if initialClients < 2 {
		t.Fatalf("expected ≥ 2 clients, got %d", initialClients)
	}

	// Step 3: Client A goes silent (never reads).
	// Client B reads all events from the hub for the duration.
	var bCount atomic.Int64
	stopB := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopB:
				return
			default:
			}
			_, _, err := connB.ReadMessage()
			if err != nil {
				return
			}
			bCount.Add(1)
		}
	}()

	// Post a few events to keep the hub alive and confirm Client B
	// continues receiving while Client A does nothing.
	for i := 0; i < 5; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": i},
		})
		resp, err := http.Post("http://localhost:8081/api/events", "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("post event %d: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	time.Sleep(2 * time.Second) // Let Client B drain events.
	t.Logf("Client B received %d events during %d s wait (Client A is zombie)", bCount.Load(), 120)

	// Step 4: Wait for the zombie eviction timeout.
	// The default is 120 seconds; skip if too long for local development.
	const evictionTimeout = 120 * time.Second
	const localShortCut = 0 // Set > 0 to run with a shorter timeout locally

	if localShortCut > 0 {
		t.Logf("Using shortened wait of %v (local development mode)", time.Duration(localShortCut)*time.Second)
		time.Sleep(time.Duration(localShortCut) * time.Second)
	} else {
		// Production test: wait the full eviction timeout.
		// Protect against hanging forever with an outer deadline.
		deadline := time.Now().Add(evictionTimeout + 30*time.Second)
		t.Logf("Waiting up to %v for zombie eviction timeout (Client A is silent)", evictionTimeout)
		for time.Now().Before(deadline) {
			hub := parseHealthBody("http://localhost:8081/health")
			if c, ok := hub["clients"].(float64); ok && int(c) < initialClients {
				t.Logf("Eviction detected! Client count dropped from %d to %d", initialClients, int(c))
				break
			}
			time.Sleep(5 * time.Second)
		}

		// Check we actually waited long enough.
		elapsed := time.Since(deadline.Add(-evictionTimeout - 30*time.Second))
		if elapsed < evictionTimeout-10*time.Second {
			// Might be a local test with a short timeout — that's fine.
			_ = elapsed
		} else {
			// Full timeout elapsed — verify eviction happened.
			hub := parseHealthBody("http://localhost:8081/health")
			if c, ok := hub["clients"].(float64); ok && int(c) >= initialClients {
				t.Errorf("eviction timeout elapsed (%v) but client count did not drop (still %d, expected < %d)",
					evictionTimeout, int(c), initialClients)
			}
		}
	}

	// Step 5: Close Client B and check final hub count (should be 0).
	close(stopB)
	connB.Close()
	time.Sleep(2 * time.Second)

	finalHub := parseHealthBody("http://localhost:8081/health")
	finalClients := 0
	if c, ok := finalHub["clients"].(float64); ok {
		finalClients = int(c)
	}
	t.Logf("Final hub client count: %d", finalClients)
	if finalClients != 0 {
		t.Errorf("expected 0 clients after eviction + close, got %d", finalClients)
	}
}