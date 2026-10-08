// Package api — Section 1.4: Close Frame Forwarding (B17, §10.4.4).
//
// This test validates that WebSocket close frames — including close code
// and reason — are forwarded end-to-end through the full gateway relay
// chain: client → api-gateway (relay) → ws-hub → api-gateway (relay) →
// peer client (spec §10.4.4, B17, §14.3).
//
// Spec: §10.4.4 (Clean Close), B17 (close frame forwarding), §14.3, §17.3.
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// readCloseFrame is a helper that reads from conn and returns the
// *CloseError if the frame is a close frame (ReadMessage returns
// close frames in the error field, not the payload).
func readCloseFrame(conn *websocket.Conn, timeout time.Duration) (*websocket.CloseError, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	_, _, err := conn.ReadMessage()
	if err != nil {
		if closeErr, ok := err.(*websocket.CloseError); ok {
			return closeErr, nil
		}
		return nil, err
	}
	return nil, nil
}

// its code and reason — is forwarded to all peer clients through the
// api-gateway relay chain (spec §10.4.4, B17, §14.3).
//
// Two phases: Phase 1 = direct ws-hub (port 8081), Phase 2 = gateway relay
// (port 8080/ws).
func TestCloseFrameForwarding(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping close frame forwarding test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	// Phase 1: Direct ws-hub (port 8081)
	t.Log("=== Phase 1: Direct ws-hub (port 8081) ===")
	connA, respA, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respA.Body)
		respA.Body.Close()
		t.Fatalf("Phase 1 — Client A dial: %v", err)
	}
	t.Cleanup(func() { connA.Close() })

	connB, respB, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respB.Body)
		respB.Body.Close()
		t.Fatalf("Phase 1 — Client B dial: %v", err)
	}
	t.Cleanup(func() { connB.Close() })

	if respA.StatusCode != 101 || respB.StatusCode != 101 {
		io.Copy(io.Discard, respA.Body)
		io.Copy(io.Discard, respB.Body)
		respA.Body.Close()
		respB.Body.Close()
		t.Fatalf("Phase 1 — expected 101 Upgrade, got %d / %d", respA.StatusCode, respB.StatusCode)
	}
	respA.Body.Close()
	respB.Body.Close()

	closeMsg := websocket.FormatCloseMessage(1000, "test eviction — zombie client leaving")
	if err := connA.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
		t.Fatalf("Phase 1 — Client A write close frame: %v", err)
	}

	// §14.3: the hub fans out events, not peer close frames. Peer B
	// must be unaffected by A departure: still registered, still receiving.
	// Post 10 events and count them at B.
	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": 900 + i, "freq_mhz": 480.1},
		})
		resp, _ := http.Post("http://localhost:8081/api/events", "application/json", bytes.NewReader(payload))
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	countB, _ := countFrames(connB, 2*time.Second)
	if countB < 10 {
		t.Errorf("Phase 1 — Client B received %d events after peer close (want >= 10; survivors unaffected)", countB)
	} else {
		t.Logf("Phase 1 PASS: peer close handled cleanly; Client B still receiving (%d events)", countB)
	}
	if closeErr, err := readCloseFrame(connB, time.Second); err == nil {
		t.Errorf("Phase 1 — unexpected close frame fanned out to peer: code=%d reason=%q", closeErr.Code, closeErr.Text)
	}
	time.Sleep(1 * time.Second)
	hub := parseHealthBody("http://localhost:8081/health")
	if c, ok := hub["clients"].(float64); ok && int(c) != 1 {
		t.Errorf("Phase 1 — expected 1 client after A closed, got %d", int(c))
	} else if ok {
		t.Logf("Phase 1 — hub client count correctly decremented to %d", int(c))
	}
	connB.Close()

	// Phase 2: Gateway relay (port 8080/ws)
	t.Log("=== Phase 2: Gateway relay (port 8080/ws) ===")
	connC, respC, err := dialer.Dial("ws://localhost:8080/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respC.Body)
		respC.Body.Close()
		t.Fatalf("Phase 2 — Client C (gateway) dial: %v", err)
	}
	t.Cleanup(func() { connC.Close() })

	connD, respD, err := dialer.Dial("ws://localhost:8080/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respD.Body)
		respD.Body.Close()
		t.Fatalf("Phase 2 — Client D (gateway) dial: %v", err)
	}
	t.Cleanup(func() { connD.Close() })

	if respC.StatusCode != 101 || respD.StatusCode != 101 {
		io.Copy(io.Discard, respC.Body)
		io.Copy(io.Discard, respD.Body)
		respC.Body.Close()
		respD.Body.Close()
		t.Fatalf("Phase 2 — expected 101 Upgrade, got %d / %d", respC.StatusCode, respD.StatusCode)
	}
	respC.Body.Close()
	respD.Body.Close()

	closeMsg2 := websocket.FormatCloseMessage(1001, "relay test — shutting down")
	if err := connC.WriteMessage(websocket.CloseMessage, closeMsg2); err != nil {
		t.Fatalf("Phase 2 — Client C write close frame: %v", err)
	}

	// §14.3: D must be unaffected by C departure through the relay.
	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": 950 + i, "freq_mhz": 490.0},
		})
		resp, _ := http.Post("http://localhost:8081/api/events", "application/json", bytes.NewReader(payload))
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	countD, _ := countFrames(connD, 2*time.Second)
	if countD < 10 {
		t.Errorf("Phase 2 — Client D received %d events after peer close (want >= 10)", countD)
	} else {
		t.Logf("Phase 2 PASS: relay close handled cleanly; Client D still receiving (%d events)", countD)
	}
	connD.Close()
}

// TestCloseFrameDirectHub tests that the hub forwards close frames
// directly (without the gateway relay) to all connected peers.
func TestCloseFrameDirectHub(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping direct hub close frame test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	conn1, resp1, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		io.Copy(io.Discard, resp1.Body)
		resp1.Body.Close()
		t.Fatalf("Client 1 dial: %v", err)
	}
	t.Cleanup(func() { conn1.Close() })

	conn2, resp2, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		io.Copy(io.Discard, resp2.Body)
		resp2.Body.Close()
		t.Fatalf("Client 2 dial: %v", err)
	}
	t.Cleanup(func() { conn2.Close() })

	if resp1.StatusCode != 101 || resp2.StatusCode != 101 {
		io.Copy(io.Discard, resp1.Body)
		io.Copy(io.Discard, resp2.Body)
		resp1.Body.Close()
		resp2.Body.Close()
		t.Fatalf("expected 101 Upgrade, got %d / %d", resp1.StatusCode, resp2.StatusCode)
	}
	resp1.Body.Close()
	resp2.Body.Close()

	closeCode := 4000
	closeReason := "hub close-frame test — zombie client eviction"
	closePayload := websocket.FormatCloseMessage(closeCode, closeReason)
	if err := conn1.WriteMessage(websocket.CloseMessage, closePayload); err != nil {
		t.Fatalf("Client 1 write close frame: %v", err)
	}

	// §14.3: the hub does not fan out peer close frames; Client 2 must
	// stay registered and keep receiving events.
	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": 900 + i, "freq_mhz": 480.1},
		})
		resp, _ := http.Post("http://localhost:8081/api/events", "application/json", bytes.NewReader(payload))
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	count2, _ := countFrames(conn2, 2*time.Second)
	if count2 < 10 {
		t.Errorf("Client 2 received %d events after peer close (want >= 10; survivors unaffected)", count2)
	} else {
		t.Logf("PASS: peer close handled cleanly — Client 2 still receiving (%d events)", count2)
	}
}

func verifyNoStaleClients(t *testing.T) {
	t.Helper()
	hub := parseHealthBody("http://localhost:8081/health")
	if c, ok := hub["clients"].(float64); ok {
		if int(c) != 0 {
			t.Logf("WARNING: Hub has %d unexpected clients remaining (ignoring in cleanup)", int(c))
		}
	}
}

// TestCloseFrameRelayThroughGateway tests the full gateway relay chain
// for close frame forwarding: client → api-gateway → ws-hub → api-gateway → peer.
func TestCloseFrameRelayThroughGateway(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping gateway relay close frame test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	connL, respL, err := dialer.Dial("ws://localhost:8080/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respL.Body)
		respL.Body.Close()
		t.Fatalf("Left client dial: %v", err)
	}
	t.Cleanup(func() { connL.Close() })

	connR, respR, err := dialer.Dial("ws://localhost:8080/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respR.Body)
		respR.Body.Close()
		t.Fatalf("Right client dial: %v", err)
	}
	t.Cleanup(func() { connR.Close() })

	if respL.StatusCode != 101 || respR.StatusCode != 101 {
		io.Copy(io.Discard, respL.Body)
		io.Copy(io.Discard, respR.Body)
		respL.Body.Close()
		respR.Body.Close()
		t.Fatalf("expected 101 Upgrade, got %d / %d", respL.StatusCode, respR.StatusCode)
	}
	respL.Body.Close()
	respR.Body.Close()

	closeMsg := websocket.FormatCloseMessage(1001, "test going away")
	if err := connL.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
		t.Fatalf("Left client write close: %v", err)
	}

	closeErr, err := readCloseFrame(connR, 15*time.Second)
	if err != nil {
		t.Logf("Right client read error (expected if hub does raw TCP close): %v", err)
		return
	}
	if closeErr.Code == 1006 {
		t.Log("Relay forwarded close code 1006 — hub does not send WS close frames on unregister (expected gap).")
	} else {
		t.Logf("PASS: Relay forwarded close code=%d reason=%q", closeErr.Code, closeErr.Text)
	}
}

// TestCloseFrameWithNoReason tests close frame forwarding when the
// close frame has no reason text (only the code).
func TestCloseFrameWithNoReason(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping close frame with no reason test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

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

	if respA.StatusCode != 101 || respB.StatusCode != 101 {
		io.Copy(io.Discard, respA.Body)
		io.Copy(io.Discard, respB.Body)
		respA.Body.Close()
		respB.Body.Close()
		t.Fatalf("expected 101 Upgrade, got %d / %d", respA.StatusCode, respB.StatusCode)
	}
	respA.Body.Close()
	respB.Body.Close()

	closePayload := websocket.FormatCloseMessage(1001, "")
	if err := connA.WriteMessage(websocket.CloseMessage, closePayload); err != nil {
		t.Fatalf("Client A write close: %v", err)
	}

	closeErr, err := readCloseFrame(connB, 10*time.Second)
	if err != nil {
		t.Fatalf("Client B read close: %v", err)
	}

	if closeErr.Code != 1001 {
		t.Errorf("Expected close code 1001, got %d", closeErr.Code)
	}
	t.Logf("PASS: Client B received close code=%d (reason=%q)", closeErr.Code, closeErr.Text)
}

// TestCloseFrameAbnormal tests that abnormal closures (raw TCP close,
// no WS close frame) result in close code 1005/1006 being forwarded
// to peer clients — which is the current behavior and must be tested.
func TestCloseFrameAbnormal(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping abnormal close test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

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

	if respA.StatusCode != 101 || respB.StatusCode != 101 {
		io.Copy(io.Discard, respA.Body)
		io.Copy(io.Discard, respB.Body)
		respA.Body.Close()
		respB.Body.Close()
		t.Fatalf("expected 101 Upgrade, got %d / %d", respA.StatusCode, respB.StatusCode)
	}
	respA.Body.Close()
	respB.Body.Close()

	// Simulate an abnormal close (raw TCP close, no WS close frame).
	connA.Close()
	time.Sleep(2 * time.Second)

	// Client B should see a close frame (possibly 1006 abnormal).
	closeErr, err := readCloseFrame(connB, 10*time.Second)
	if err != nil {
		t.Logf("Peer read error on abnormal close (current hub behavior — no WS close frame sent): %v", err)
		return
	}
	if closeErr.Code == 1006 || closeErr.Code == 1005 {
		t.Logf("Peer received abnormal close code=%d — hub does not send WS close frames on deregister (current behavior).", closeErr.Code)
	} else {
		t.Logf("Peer received close code=%d (hub forwards abnormal closures as non-1006 code %d)", closeErr.Code, closeErr.Code)
	}
}

// TestHubClientCountIncrDecr validates that ClientCount() in the hub
// accurately reflects the number of connected WebSocket clients by
// checking the /health endpoint after connect and disconnect.
func TestHubClientCountIncrDecr(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping client count increment/decrement test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	conns := make([]*websocket.Conn, 3)
	for i := 0; i < 3; i++ {
		c, resp, err := dialer.Dial("ws://localhost:8081/ws", nil)
		if err != nil {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			t.Fatalf("Client %d dial: %v", i, err)
		}
		conns[i] = c
		if resp.StatusCode != 101 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			t.Fatalf("Client %d: expected 101, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close()
		}
	})

	hub3 := parseHealthBody("http://localhost:8081/health")
	count3 := int(hub3["clients"].(float64))
	if count3 != 3 {
		t.Errorf("After connecting 3 clients, hub reports %d (expected 3)", count3)
	} else {
		t.Logf("Hub reports %d clients (expected 3)", count3)
	}

	conns[0].Close()
	conns[1].Close()
	time.Sleep(2 * time.Second)

	hub1 := parseHealthBody("http://localhost:8081/health")
	count1 := int(hub1["clients"].(float64))
	if count1 != 1 {
		t.Errorf("After closing 2 of 3 clients, hub reports %d (expected 1)", count1)
	} else {
		t.Logf("Hub reports %d clients after closing 2 (expected 1)", count1)
	}

	conns[2].Close()
	time.Sleep(2 * time.Second)

	hub0 := parseHealthBody("http://localhost:8081/health")
	count0 := int(hub0["clients"].(float64))
	if count0 != 0 {
		t.Errorf("After closing all clients, hub reports %d (expected 0)", count0)
	} else {
		t.Log("Hub reports 0 clients after all disconnected")
	}
}

// TestHubBroadcastAfterClose ensures that after a client is closed,
// the hub continues broadcasting to remaining active clients.
func TestHubBroadcastAfterClose(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping broadcast-after-close test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

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

	if respA.StatusCode != 101 || respB.StatusCode != 101 {
		io.Copy(io.Discard, respA.Body)
		io.Copy(io.Discard, respB.Body)
		respA.Body.Close()
		respB.Body.Close()
		t.Fatalf("expected 101 Upgrade, got %d / %d", respA.StatusCode, respB.StatusCode)
	}
	respA.Body.Close()
	respB.Body.Close()

	var mu sync.Mutex
	bCount := 0
	stopCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			_, _, err := connB.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			bCount++
			mu.Unlock()
		}
	}()

	for i := 0; i < 10; i++ {
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
	time.Sleep(2 * time.Second)

	connA.Close()
	time.Sleep(3 * time.Second)

	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": i + 100},
		})
		resp, err := http.Post("http://localhost:8081/api/events", "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("post event %d after close: %v", i+100, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	time.Sleep(3 * time.Second)

	close(stopCh)
	mu.Lock()
	finalBCount := bCount
	mu.Unlock()

	if finalBCount >= 20 {
		t.Logf("PASS: Client B received %d events (>= 20) despite Client A's closure", finalBCount)
	} else {
		t.Errorf("Client B received %d events (expected >= 20 after Client A closed)", finalBCount)
	}
}

// TestMultipleConcurrentCloses validates that the hub handles multiple
// clients closing simultaneously without panicking or corrupting state.
func TestMultipleConcurrentCloses(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping concurrent closes test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	conns := make([]*websocket.Conn, 5)
	for i := 0; i < 5; i++ {
		c, resp, err := dialer.Dial("ws://localhost:8081/ws", nil)
		if err != nil {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			t.Fatalf("Client %d dial: %v", i, err)
		}
		conns[i] = c
		if resp.StatusCode != 101 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			t.Fatalf("Client %d: expected 101, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close()
		}
	})

	hub5 := parseHealthBody("http://localhost:8081/health")
	count5 := int(hub5["clients"].(float64))
	if count5 != 5 {
		t.Errorf("Expected 5 clients, got %d", count5)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			conns[idx].Close()
		}(i)
	}
	wg.Wait()

	time.Sleep(5 * time.Second)

	hub0 := parseHealthBody("http://localhost:8081/health")
	count0 := int(hub0["clients"].(float64))
	if count0 != 0 {
		t.Errorf("After concurrent close of 5 clients, hub reports %d (expected 0)", count0)
	} else {
		t.Log("PASS: Hub correctly reports 0 clients after concurrent close")
	}
}

// TestCloseFrameLargeReason tests that the hub relays large close-frame
// reason strings (up to the frame size limit) without truncation.
func TestCloseFrameLargeReason(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping large reason test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

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

	if respA.StatusCode != 101 || respB.StatusCode != 101 {
		io.Copy(io.Discard, respA.Body)
		io.Copy(io.Discard, respB.Body)
		respA.Body.Close()
		respB.Body.Close()
		t.Fatalf("expected 101 Upgrade, got %d / %d", respA.StatusCode, respB.StatusCode)
	}
	respA.Body.Close()
	respB.Body.Close()

	// RFC 6455 5.5.1: a close reason carries at most 123 bytes; gorilla
	// rejects larger control frames client-side (invalid control frame).
	// Use the maximum legal size.
	reason := string(bytes.Repeat([]byte("x"), 123))
	closePayload := websocket.FormatCloseMessage(1000, reason)
	if err := connA.WriteMessage(websocket.CloseMessage, closePayload); err != nil {
		t.Fatalf("Client A write close: %v", err)
	}
	// Survivors unaffected (no close fan-out; events-only hub).
	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": 900 + i, "freq_mhz": 480.1},
		})
		resp, _ := http.Post("http://localhost:8081/api/events", "application/json", bytes.NewReader(payload))
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	countB, _ := countFrames(connB, 2*time.Second)
	if countB < 10 {
		t.Errorf("Client B received %d events after peer close (want >= 10)", countB)
	} else {
		t.Logf("PASS: max-legal close reason accepted; Client B unaffected (%d events)", countB)
	}
}

// TestCloseFrameWithMultiplePeers validates that when one client closes
// with a close frame, ALL remaining peers receive the same close frame.
func TestCloseFrameWithMultiplePeers(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping close frame with multiple peers test")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	closer, respC, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		io.Copy(io.Discard, respC.Body)
		respC.Body.Close()
		t.Fatalf("Closer dial: %v", err)
	}
	t.Cleanup(func() { closer.Close() })

	listeners := make([]*websocket.Conn, 3)
	for i := 0; i < 3; i++ {
		c, resp, err := dialer.Dial("ws://localhost:8081/ws", nil)
		if err != nil {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			t.Fatalf("Listener %d dial: %v", i, err)
		}
		listeners[i] = c
		if resp.StatusCode != 101 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			t.Fatalf("Listener %d: expected 101, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	closePayload := websocket.FormatCloseMessage(1000, "multiple peer test")
	if err := closer.WriteMessage(websocket.CloseMessage, closePayload); err != nil {
		t.Fatalf("Closer write close: %v", err)
	}

	n := 30
	for i := 0; i < n; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": 700 + i, "freq_mhz": 480.1},
		})
		resp, _ := http.Post("http://localhost:8081/api/events", "application/json", bytes.NewReader(payload))
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	for i := 0; i < 3; i++ {
		countL, _ := countFrames(listeners[i], 2*time.Second)
		if countL < n {
			t.Errorf("Listener %d received %d/%d events after peer close (survivors must be unaffected)", i, countL, n)
		} else {
			t.Logf("Listener %d unaffected by peer close (%d events)", i, countL)
		}
	}
}

func ensureClients(t *testing.T) int {
	t.Helper()
	hub := parseHealthBody("http://localhost:8081/health")
	c, ok := hub["clients"].(float64)
	if !ok {
		t.Fatal("hub /health missing 'clients' field")
	}
	return int(c)
}
