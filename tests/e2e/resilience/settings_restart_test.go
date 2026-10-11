// Package resilience — Test 5.2 step 4: settings round-trip persistence
// across an api-gateway restart (E2E-TEST-SUITE §4.5, Test 5.2). Steps
// 1–3 run in the Playwright suite (settings_and_config.test.ts 5.1.2);
// this covers the restart/reload half the browser cannot do: a saved
// value must still be served — reloaded from the YAML file, the single
// source of truth (§17.1) — after the settings authority itself
// restarts. A regression that keeps settings only in gateway memory
// fails here.
package resilience

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	// settingsRestartSection/Key is the knob the round-trip flips: a
	// harmless scalar (peak threshold) whose save rewrites
	// config/signal-processor.yaml without disturbing live capture.
	settingsRestartSection = "processing"
	settingsRestartKey     = "peak_detection.threshold_db"
	settingsRestartDelta   = 3.0
)

// fetchSettingsThreshold GETs the settings index and returns the
// current value of the round-trip knob.
func fetchSettingsThreshold(t *testing.T) float64 {
	t.Helper()
	resp, err := http.Get("http://localhost:8080/api/settings")
	if err != nil {
		t.Fatalf("GET /api/settings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		t.Fatalf("GET /api/settings → %d, want 200", resp.StatusCode)
	}
	var doc struct {
		Values map[string]map[string]any `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode settings index: %v", err)
	}
	sec, ok := doc.Values[settingsRestartSection]
	if !ok || sec == nil {
		t.Fatalf("settings index has no %q section: %#v",
			settingsRestartSection, doc.Values)
	}
	raw, ok := sec[settingsRestartKey]
	if !ok {
		t.Fatalf("settings index missing key %q in section %q (keys: %v)",
			settingsRestartKey, settingsRestartSection, mapKeys(sec))
	}
	v, ok := raw.(float64)
	if !ok {
		t.Fatalf("settings key %q = %#v, want a number", settingsRestartKey, raw)
	}
	return v
}

// putSettingsThreshold PUTs one value for the round-trip knob.
func putSettingsThreshold(t *testing.T, v float64) {
	t.Helper()
	body := fmt.Sprintf(`{"values":{%q:%v}}`, settingsRestartKey, v)
	req, err := http.NewRequest(http.MethodPut,
		"http://localhost:8080/api/settings/"+settingsRestartSection,
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/settings/%s: %v", settingsRestartSection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %q = %v → %d, want 200 (%.200s)",
			settingsRestartKey, v, resp.StatusCode, b)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSettingsPersistAcrossGatewayRestart: save a value, restart
// api-gateway, verify the saved value is reloaded from file (Test 5.2
// step 4). Requires the docker compose stack (restart mechanism) and
// restores the original value afterwards.
func TestSettingsPersistAcrossGatewayRestart(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}
	ensureAllServicesUp(t)
	if _, err := execDockerCompose("--version"); err != nil {
		t.Skip("docker compose not available")
	}

	orig := fetchSettingsThreshold(t)
	changed := orig + settingsRestartDelta
	t.Cleanup(func() { putSettingsThreshold(t, orig) }) // best-effort restore

	// Step 2 (of the round-trip): save the changed value.
	putSettingsThreshold(t, changed)
	if got := fetchSettingsThreshold(t); math.Abs(got-changed) > 1e-9 {
		t.Fatalf("save did not take effect: %s = %v, want %v",
			settingsRestartKey, got, changed)
	}
	t.Logf("saved %s = %v (was %v)", settingsRestartKey, changed, orig)

	// Step 4: restart the settings authority itself.
	execDockerCompose("restart", "api-gateway")
	if err := waitForHTTP("http://localhost:8080/health", 60*time.Second); err != nil {
		t.Fatalf("Gateway did not recover within 60 s: %v", err)
	}

	// Values must match saved state — reloaded from the YAML file.
	if got := fetchSettingsThreshold(t); math.Abs(got-changed) > 1e-9 {
		t.Fatalf("Test 5.2 step 4 FAILED: after api-gateway restart %s = %v, "+
			"want %v — settings did not reload from file",
			settingsRestartKey, got, changed)
	}
	t.Logf("PASS: %s = %v survived an api-gateway restart (reloaded from file)",
		settingsRestartKey, changed)
}
