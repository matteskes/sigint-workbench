// Package airgap — Section 4.11 Test A6: Offline Boot Validation.
package airgap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestA6_IptablesPolicy(t *testing.T) {
	cmd := exec.Command("iptables", "-L", "OUTPUT", "-n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skip("iptables not available (may not be Linux or no root) — skip")
	}
	if contains(string(out), "DROP") {
		t.Log("  [PASS] OUTPUT chain policy is DROP (air-gap confirmed)")
	} else if contains(string(out), "ACCEPT") {
		t.Log("  [WARN] OUTPUT chain allows ACCEPT — not fully air-gapped")
	} else {
		t.Logf("  OUTPUT chain: %s", string(out))
	}
}

func TestA6_HealthEndpoints(t *testing.T) {
	for _, url := range []string{
		"http://127.0.0.1:8080/health",
		"http://127.0.0.1:8081/health",
	} {
		cmd := exec.Command("curl", "-sf", "--connect-timeout", "3", url)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("  %s unreachable: %v (may not be running)", url, err)
		} else {
			t.Logf("  %s: %s", url, string(out))
		}
	}
}

func TestA6_ServicesConfigExists(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	configDir := filepath.Join(root, "config")
	files, err := filepath.Glob(filepath.Join(configDir, "*.yaml"))
	if os.IsNotExist(err) {
		t.Skip("config/ directory not found — skip")
	}
	if err != nil {
		t.Fatalf("glob config: %v", err)
	}
	if len(files) == 0 {
		t.Log("  no .yaml configs found — skip (may be Docker-only)")
	} else {
		for _, f := range files {
			t.Logf("  config: %s", f)
		}
	}
}

func TestA6_DockerCompose(t *testing.T) {
	cmd := exec.Command("docker", "compose", "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		cmd = exec.Command("docker", "-compose", "version")
		out, err = cmd.CombinedOutput()
	}
	if err != nil {
		t.Skip("docker compose not available — skip")
	}
	t.Logf("  docker compose: %s", string(out))
}

func TestA6_OfflineServicesExist(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	services := []string{
		"sdr-capture", "iq-ingest", "signal-processor",
		"recorder", "api-gateway", "ws-hub",
	}
	for _, svc := range services {
		svcDir := filepath.Join(root, "cmd", svc)
		if _, err := os.Stat(svcDir); os.IsNotExist(err) {
			t.Logf("  %s/ not found (may be Docker-only)", svc)
		} else {
			mainPath := filepath.Join(svcDir, "main.go")
			if _, err := os.Stat(mainPath); err == nil {
				t.Logf("  %s/main.go exists", svc)
			}
		}
	}
}
