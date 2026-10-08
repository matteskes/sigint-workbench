// Package resilience — Test R1: Service Restart Resilience (B5 extension).
package resilience

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

func drainConn(ws *websocket.Conn) {
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			ws.SetReadDeadline(time.Time{})
			return
		}
	}
}

func pumpEvents(count, start int) {
	for i := 0; i < count; i++ {
		payload, _ := json.Marshal(map[string]any{
			"type":    "signal.new",
			"payload": map[string]any{"seq": start + i, "freq_mhz": float64(480 + i)},
		})
		resp, _ := http.Post("http://localhost:8081/api/events",
			"application/json", bytes.NewReader(payload))
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
}

func readFrames(ws *websocket.Conn, timeout time.Duration) int {
	count := 0
	for {
		ws.SetReadDeadline(time.Now().Add(timeout))
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
		count++
	}
	return count
}

// TestServiceRestartSingle: restart api-gateway, reconnect WS, verify event flow (spec §4.10.3).
func TestServiceRestartSingle(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}
	ensureAllServicesUp(t)

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	client, resp, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		t.Fatalf("WS dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	if resp.StatusCode != 101 {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	pumpEvents(10, 0)
	received := readFrames(client, 5*time.Second)
	if received < 5 {
		t.Fatalf("expected 5+ events, got %d", received)
	}
	t.Logf("WS connected, %d events delivered", received)

	if _, err := execDockerCompose("--version"); err != nil {
		t.Skip("docker compose not available")
	}
	execDockerCompose("restart", "api-gateway")

	if err := waitForHTTP("http://localhost:8080/health", 60*time.Second); err != nil {
		t.Fatalf("Gateway did not recover within 60 s: %v", err)
	}

	newClient, newResp, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		if newResp != nil {
			io.Copy(io.Discard, newResp.Body)
			newResp.Body.Close()
		}
		t.Fatalf("WS reconnect: %v", err)
	}
	t.Cleanup(func() { newClient.Close() })
	if newResp.StatusCode != 101 {
		io.Copy(io.Discard, newResp.Body)
		newResp.Body.Close()
		t.Fatalf("expected 101 on reconnect, got %d", newResp.StatusCode)
	}
	newResp.Body.Close()

	pumpEvents(10, 100)
	n := readFrames(newClient, 5*time.Second)
	if n < 5 {
		t.Fatalf("events did not resume: received %d", n)
	}
	t.Logf("PASS: Restarted, reconnected, %d events resumed", n)
}

// TestServiceRestartAll: restart ALL services simultaneously, recover within 120 s (spec §4.10.3).
func TestServiceRestartAll(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}
	ensureAllServicesUp(t)

	if _, err := execDockerCompose("--version"); err != nil {
		t.Skip("docker compose not available")
	}
	execDockerCompose("restart")

	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if serviceReady() {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !serviceReady() {
		t.Fatal("Not all services recovered within 120 s")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	client, resp, err := dialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		t.Fatalf("WS dial after full restart: %v", err)
	}
	if resp.StatusCode != 101 {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); pumpEvents(10, 0) }()
	wg.Wait()

	received := readFrames(client, 5*time.Second)
	if received < 5 {
		t.Fatalf("events did not resume: received %d", received)
	}
	t.Logf("PASS: Full restart recovered, %d events resumed", received)
}

// TestServiceRestartPartialGateway: after api-gateway restart, /api/setup/status shows all "ok".
func TestServiceRestartPartialGateway(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}

	if _, err := execDockerCompose("--version"); err != nil {
		t.Skip("docker compose not available")
	}

	execDockerCompose("restart", "api-gateway")

	deadline := time.Now().Add(60 * time.Second)
	var allOK bool
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://localhost:8080/api/setup/status")
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var result map[string]any
			if err := json.Unmarshal(body, &result); err == nil {
				if components, ok := result["components"].(map[string]any); ok {
					allOK = true
					for _, info := range components {
						infoMap, ok2 := info.(map[string]any)
						if !ok2 {
							allOK = false
							break
						}
						if status, ok3 := infoMap["status"].(string); !ok3 || status != "ok" {
							allOK = false
							break
						}
					}
				}
			}
		}
		if allOK {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if !allOK {
		t.Fatal("Gateway /api/setup/status did not report all components as \"ok\" within 60 s")
	}
	t.Log("PASS: All components recovered as \"ok\" after partial restart")
}
