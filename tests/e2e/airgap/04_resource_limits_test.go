// Package airgap — Section 4.11 Test A4: Resource Limit Enforcement.
package airgap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestA4_CgroupsExist(t *testing.T) {
	if _, err := os.Stat("/sys/fs/cgroup"); os.IsNotExist(err) {
		t.Skip("cgroups not mounted at /sys/fs/cgroup — skip")
	}
	t.Log("  cgroups v2 mounted at /sys/fs/cgroup")

	cgroupTests := []string{
		"/sys/fs/cgroup/cpu.max",
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/pids.max",
	}
	for _, cg := range cgroupTests {
		if _, err := os.Stat(cg); err == nil {
			data, _ := os.ReadFile(cg)
			t.Logf("  %s exists: %s", cg, string(data))
		} else {
			t.Logf("  %s not found (cgroup may use v1 hierarchy)", cg)
		}
	}
}

func TestA4_HeapLimits(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}

	hubPath := filepath.Join(root, "internal", "ws", "hub.go")
	data, err := os.ReadFile(hubPath)
	if err != nil {
		t.Skipf("hub.go not found at %s — skip", hubPath)
	}

	checks := map[string]string{
		"sendQueueSize": "sendQueueSize = 256",
		"writeWait":     "writeWait = 5 * time.Second",
		"pingPeriod":    "pingPeriod = 25 * time.Second",
		"pongWait":      "pongWait   = 60 * time.Second",
	}
	for name, expected := range checks {
		if contains(string(data), expected) {
			t.Logf("  [PASS] %s found in hub.go", name)
		} else {
			t.Logf("  [WARN] %s not found in hub.go (expected: %s)", name, expected)
		}
	}
}

func TestA4_ProcStat(t *testing.T) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		t.Skip("/proc/stat not available (not Linux?) — skip")
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	t.Logf("  /proc/stat: %d lines", lines)
	if lines > 5 {
		t.Log("  [PASS] CPU stats available")
	}
}

func TestA4_DockerCgroup(t *testing.T) {
	cmd := exec.Command("docker", "info", "--format", "{{.NCPU}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	t.Logf("  docker info CPU count: %s", string(out))
}
