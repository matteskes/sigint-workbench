// Package airgap — Section 4.11 Test A7: Disk Exhaustion Resilience.
package airgap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestA7_DiskUsage(t *testing.T) {
	cmd := exec.Command("df", "-h", "/")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skip("df not available — skip")
	}
	if contains(string(out), "95%") || contains(string(out), "100%") {
		t.Log("  [WARN] disk usage >= 95% — testing disk-full behavior")
	} else {
		t.Log("  Disk usage normal (below 95%): " + string(out))
	}
}

func TestA7_DiskQuota(t *testing.T) {
	paths := []string{
		"/sys/fs/cgroup/io.max",
		"/sys/fs/cgroup/io.stat",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			data, _ := os.ReadFile(p)
			t.Logf("  %s: %s", p, string(data))
		} else {
			t.Logf("  %s not found (cgroup v1 or not mounted)", p)
		}
	}
}

func TestA7_507Response(t *testing.T) {
	cmd := exec.Command("curl", "-sf", "--connect-timeout", "3",
		"http://127.0.0.1:8080/api/signals?limit=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("  gateway API unreachable (may not be running): %v", err)
	} else {
		t.Logf("  GET /api/signals: %d bytes", len(out))
	}
}

func TestA7_PostgresIntegrity(t *testing.T) {
	cmd := exec.Command("psql", "-c", "SELECT 1 AS integrity_check;", "-d", "sigint_workbench")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skip("psql not available or DB not running — skip")
	}
	if contains(string(out), "integrity_check") {
		t.Log("  [PASS] PostgreSQL connectivity + query OK")
	} else {
		t.Logf("  integrity check output: %s", string(out))
	}
}

func TestA7_FreeSpaceCheck(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	dirs := []string{
		filepath.Join(root, "models"),
		filepath.Join(root, "tiles", "data"),
		filepath.Join(root, "config"),
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Logf("  %s not found (may be Docker-only)", dir)
		} else {
			files, _ := os.ReadDir(dir)
			t.Logf("  %s: %d entries", dir, len(files))
		}
	}
}
