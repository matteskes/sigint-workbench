// Package api — shared test infrastructure for all P0 API-layer tests
// (1.1 Health Probes, 1.2 Backpressure, 3.1 Close Code, 5.1 Settings).
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// findRoot returns the project root by searching for go.mod upward from cwd.
func findRoot() string {
	root, _ := os.Getwd()
	for {
		next := filepath.Dir(root)
		if next == root {
			break
		}
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		root = next
	}
	return root
}

// healthEndpoints returns the three P0 health probe URLs, keyed by service name.
func healthEndpoints() map[string]string {
	return map[string]string{
		"gateway": "http://localhost:8080/health",
		"hub":     "http://localhost:8081/health",
		"capture": "http://127.0.0.1:9090/api/v1/status",
	}
}

// waitForHTTP polls GET on url every 500ms until it receives 200 with valid
// JSON, or until the given timeout elapses.  Returns nil on success, or a
// wrapped timeout error.
func waitForHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		// Must be valid JSON (dict or array).
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return nil
	}
	return fmt.Errorf("%s unreachable after %v", url, timeout)
}

// ensureAllServicesUp fails the test if any service is not ready, so that
// subsequent steps do not race.
func ensureAllServicesUp(t *testing.T) {
	t.Helper()
	for name, url := range healthEndpoints() {
		if err := waitForHTTP(url, 30*time.Second); err != nil {
			t.Fatalf("Service %s not ready: %v", name, err)
		}
	}
}

// parseHealthBody expects a JSON object and returns it as map[string]any,
// failing the test if the response is not 200 or not parseable.
func parseHealthBody(url string) map[string]any {
	resp, err := http.Get(url)
	if err != nil {
		panic(fmt.Sprintf("%s: %v", url, err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("%s: status %d body: %s", url, resp.StatusCode, string(body)))
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		panic(fmt.Sprintf("%s: not valid JSON: %s", url, string(body)))
	}
	return result
}

// parseHealthArray expects a JSON array of objects and returns it,
// failing the test if the response is not 200 or not parseable.
func parseHealthArray(url string) []map[string]any {
	resp, err := http.Get(url)
	if err != nil {
		panic(fmt.Sprintf("%s: %v", url, err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("%s: status %d body: %s", url, resp.StatusCode, string(body)))
	}
	var result []map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		panic(fmt.Sprintf("%s: not valid JSON: %s", url, string(body)))
	}
	return result
}

// serviceReady gates the entire test suite on three services being
// reachable.  If any service is down the test is skipped rather than
// failing — useful for CI where the full Docker stack may not be running.
func serviceReady() bool {
	for _, url := range healthEndpoints() {
		resp, err := http.Get(url)
		if err != nil {
			return false
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}
	}
	return true
}

func waitForGatewayProbeGate(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://localhost:8080/api/setup/status")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("gateway /api/setup/status unreachable after timeout")
}

// hasField asserts that the given map contains key with the expected type,
// failing the test if not.  name is used in the failure message.
func hasField(t *testing.T, m map[string]any, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("body missing key %q", key)
		return
	}
	if v == nil {
		t.Errorf("body key %q is nil", key)
	}
}

// expectStringField asserts m[key] is a non-empty string, failing the test.
func expectStringField(t *testing.T, m map[string]any, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("body missing key %q", key)
		return
	}
	s, ok := v.(string)
	if !ok || s == "" {
		t.Errorf("body key %q = %v, want non-empty string", key, v)
		return
	}
}

// expectIntField asserts m[key] is a non-negative int, failing the test.
func expectIntField(t *testing.T, m map[string]any, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("body missing key %q", key)
		return
	}
	f, ok := v.(float64)
	if !ok || f < 0 {
		t.Errorf("body key %q = %v, want non-negative number", key, v)
		return
	}
	_ = f
}

// expectSDEFieldsPresent asserts that a device-status object from
// /api/v1/status has all required capture fields.
func expectSDEFieldsPresent(t *testing.T, dev map[string]any) {
	expectFieldsPresent(t, dev,
		"id", "driver", "model", "active",
		"freq_hz", "freq_mhz", "gain_db",
		"sample_rate", "mode", "scanning", "scan_paused",
	)
}

// expectFieldsPresent asserts that all listed keys exist in the map.
func expectFieldsPresent(t *testing.T, m map[string]any, keys ...string) {
	for _, k := range keys {
		hasField(t, m, k)
	}
}

// expectBoolField asserts m[key] is a bool, failing the test.
func expectBoolField(t *testing.T, m map[string]any, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("body missing key %q", key)
		return
	}
	if _, ok := v.(bool); !ok {
		t.Errorf("body key %q = %v, want bool", key, v)
	}
}