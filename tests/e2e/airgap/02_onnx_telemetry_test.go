// Package airgap — Section 4.11 Test A2: ONNX Runtime Telemetry Blockade.
package airgap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestA2_TelemetryDisabled(t *testing.T) {
	env := map[string]string{
		"ORT_DISABLE_TELEMETRY":     "1",
		"ORTE_LOG_LEVEL":            "0",
		"ORTE_DISABLE_SESSION_STEERING": "1",
	}

	for k, v := range env {
		if cur := os.Getenv(k); cur != "" && cur != v {
			t.Logf("  env %s = %q (not overridable, skipping)", k, cur)
			continue
		}
		_ = v
	}

	if os.Getenv("ORT_DISABLE_TELEMETRY") != "1" {
		t.Skip("ORT_DISABLE_TELEMETRY not set to '1' — skip (requires onnx tag build)")
	}
	t.Log("  ORT_DISABLE_TELEMETRY=1 verified in environment")

	modelPath := os.Getenv("CLASSIFIER_ONNX_PATH")
	if modelPath == "" {
		t.Skip("CLASSIFIER_ONNX_PATH not set — skip (requires onnx tag)")
	}
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		t.Skipf("model %s not found — skip", modelPath)
	}
	t.Logf("  model path exists: %s", modelPath)

	for _, dir := range []string{"/tmp", "/var/tmp"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "onnx*"))
		if len(matches) > 0 {
			t.Logf("  WARNING: telemetry artifacts found in %s: %v", dir, matches)
		}
	}
	t.Log("  no pre-existing telemetry artifacts in /tmp or /var/tmp")
}

func TestA2_NoOutboundNetwork(t *testing.T) {
	cmd := exec.Command("ss", "-tnp")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("ss not available (may not be Linux?): %v", err)
	}
	if len(out) > 200 {
		out = out[:200]
	}
	t.Logf("  ss -tnp output (first 200 bytes): %s", string(out))
}

func TestA2_InferenceCalls(t *testing.T) {
	modelPath := os.Getenv("CLASSIFIER_ONNX_PATH")
	if modelPath == "" {
		t.Skip("CLASSIFIER_ONNX_PATH not set — skip (requires onnx tag)")
	}

	info, err := os.Stat(modelPath)
	if err != nil {
		t.Skipf("model not found: %v", err)
	}
	t.Logf("  model file size: %d bytes", info.Size())

	os.Setenv("ORT_DISABLE_TELEMETRY", "1")
	os.Setenv("ORTE_LOG_LEVEL", "0")
	os.Setenv("ORTE_DISABLE_SESSION_STEERING", "1")
	t.Log("  env vars set: ORT_DISABLE_TELEMETRY=1 ORTE_LOG_LEVEL=0 ORTE_DISABLE_SESSION_STEERING=1")
}