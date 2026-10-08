// Package api — Section 4.1: API Health & Connectivity Probes.
// Test 1.1 — HTTP Health Probe Validates Actual API (from B2).
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestHealthProbe validates that all three P0 services return HTTP 200
// with valid JSON on their health endpoints (spec §4.1.1, B2).
func TestHealthProbe(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping health probe test")
	}

	// Step 1: gateway health.
	gw := parseHealthBody("http://localhost:8080/health")
	expectStringField(t, gw, "status")
	if s, ok := gw["status"].(string); ok && s != "ok" {
		t.Errorf("status = %q, want \"ok\"", s)
	}

	// Step 2: ws-hub health.
	hub := parseHealthBody("http://localhost:8081/health")
	expectStringField(t, hub, "status")
	if s, ok := hub["status"].(string); ok && s != "ok" {
		t.Errorf("hub status = %q, want \"ok\"", s)
	}
	expectIntField(t, hub, "clients")

	// Step 3: sdr-capture control status (array of device-status objects).
	devices := parseHealthArray("http://127.0.0.1:9090/api/v1/status")
	if len(devices) == 0 {
		t.Fatal("expected at least one device in capture status")
	}
	for i, dev := range devices {
		expectSDEFieldsPresent(t, dev)
		// Validate id is a non-empty string (required for subsequent API calls).
		if id, ok := dev["id"].(string); !ok || id == "" {
			t.Errorf("device[%d].id = %v, want non-empty string", i, dev["id"])
		}
		// Validate frequency is a finite number.
		if _, ok := dev["freq_hz"].(float64); !ok {
			t.Errorf("device[%d].freq_hz = %v, want number", i, dev["freq_hz"])
		}
		// Validate scanning/scan_paused are booleans.
		expectBoolField(t, dev, "scanning")
		expectBoolField(t, dev, "scan_paused")
	}
}

// TestHealthProbeJSONFormat confirms every health endpoint returns valid
// JSON (not HTML, not plain text).  This catches wedged processes whose
// TCP backlog accepts a dial but the HTTP stack is dead.
func TestHealthProbeJSONFormat(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping JSON format check")
	}

	endpoints := []string{
		"http://localhost:8080/health",
		"http://localhost:8081/health",
		"http://127.0.0.1:9090/api/v1/status",
	}

	for _, url := range endpoints {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("%s: connection failed: %v", url, err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		// Must be parseable as JSON (dict or array).
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			t.Errorf("%s returned non-JSON: %s", url, string(body))
		}

		// Must be HTTP 200 (not 503 or 502).
		if resp.StatusCode != 200 {
			t.Errorf("%s status = %d (expected 200)", url, resp.StatusCode)
		}
	}
}

// TestHealthProbeRetry confirms waitForHTTP retries until success (or timeout).
func TestHealthProbeRetry(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping retry test")
	}

	urls := []string{
		"http://localhost:8080/health",
		"http://localhost:8081/health",
		"http://127.0.0.1:9090/api/v1/status",
	}

	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			if err := waitForHTTP(url, 5*time.Second); err != nil {
				t.Errorf("waitForHTTP(%s) failed: %v", url, err)
			}
		})
	}
}

// TestGateProbe confirms the gateway's /api/setup/status reports all
// internal components (db, ws-hub, recorder, capture) as green.
func TestGateProbe(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running — skipping gate probe test")
	}

	// Wait for the gate to be ready (the gateway must also be up).
	if err := waitForGatewayProbeGate(15 * time.Second); err != nil {
		t.Fatalf("gateway /api/setup/status not ready: %v", err)
	}

	resp, err := http.Get("http://localhost:8080/api/setup/status")
	if err != nil {
		t.Fatalf("GET /api/setup/status: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("not valid JSON: %s", string(body))
	}

	// Validate that "components" key exists and is an object.
	components, ok := result["components"].(map[string]any)
	if !ok {
		t.Fatalf("missing 'components' object in /api/setup/status")
	}

	for comp, info := range components {
		infoMap, ok := info.(map[string]any)
		if !ok {
			t.Errorf("component %q is not an object", comp)
			continue
		}
		status, _ := infoMap["status"].(string)
		if status != "ok" {
			t.Errorf("component %q status = %q (want ok)", comp, status)
		}
	}
}