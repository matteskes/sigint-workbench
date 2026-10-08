// Package resilience — Section 4.10: Resilience & Error-Format Tests (R1–R6).
//
// These tests require a fully running Docker Compose stack. When services
// are not available the entire suite is skipped (not failed).
package resilience

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------
// Helpers shared across all resilience tests
// -----------------------------------------------------------------------

func serviceReady() bool {
	for _, url := range []string{
		"http://localhost:8080/health",
		"http://localhost:8081/health",
		"http://127.0.0.1:9090/api/v1/status",
	} {
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
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return nil
	}
	return fmt.Errorf("%s unreachable after %v", url, timeout)
}

func ensureAllServicesUp(t *testing.T) {
	t.Helper()
	for _, url := range []string{
		"http://localhost:8080/health",
		"http://localhost:8081/health",
		"http://127.0.0.1:9090/api/v1/status",
	} {
		if err := waitForHTTP(url, 30*time.Second); err != nil {
			t.Fatalf("Service %s not ready: %v", url, err)
		}
	}
}

// execDockerCompose runs "docker compose" in the project root, failing the
// test (not skipping) if the binary is not found.  Callers should handle
// errors gracefully since docker compose may not be available in CI.
func execDockerCompose(args ...string) (string, error) {
	root := findRoot()
	cmd := exec.Command("docker", append([]string{"compose", "-f", root + "/docker-compose.yml"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker compose %s: %s (%w)", strings.Join(args, " "), stderr.String(), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// execDocker runs "docker" in the project root.
func execDocker(args ...string) (string, error) {
	root := findRoot()
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %s (%w)", strings.Join(args, " "), stderr.String(), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// -----------------------------------------------------------------------
// Find project root (used by execDockerCompose and execDocker).
// -----------------------------------------------------------------------

func findRoot() string {
	root, _ := os.Getwd()
	for {
		next := strings.TrimSuffix(root, "/")
		next = strings.TrimRight(next, "/")
		if next == "" {
			break
		}
		if _, err := os.Stat(next + "/go.mod"); err == nil {
			return next
		}
		root = strings.TrimSuffix(root, "/")
		root = strings.TrimRight(root, "/")
		prev := root
		idx := strings.LastIndexByte(root, '/')
		if idx < 0 {
			break
		}
		root = root[:idx]
		if root == prev {
			break
		}
	}
	return "."
}

// -----------------------------------------------------------------------
// Error-format helpers (R2)
// -----------------------------------------------------------------------

// errorKeys are the endpoints to probe for uniform {\"error\":\"...\"} responses.
var errorEndpoints = []struct {
	method string
	url    string
}{
	{"GET", "http://localhost:8080/api/signals/00000000-0000-0000-0000-000000000000"},
	{"GET", "http://localhost:8080/api/recordings/00000000-0000-0000-0000-000000000000"},
	{"GET", "http://localhost:8080/api/recordings/00000000-0000-0000-0000-000000000000/file"},
	{"PUT", "http://localhost:8080/api/sdrs/foobar"},
	{"GET", "http://localhost:8080/api/sdrs/foobar/status"},
	{"GET", "http://localhost:8080/api/signals/00000000-0000-0000-0000-000000000000/track"},
}

func parseErrorBody(body []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	return m
}

func assertHasErrorKey(t *testing.T, url, method string, status int, body []byte) {
	t.Helper()
	if status >= 400 {
		m := parseErrorBody(body)
		if m == nil {
			t.Errorf("%s %s → %d: response is not valid JSON or missing \"error\" key: %s",
				method, url, status, string(body))
			return
		}
		if _, ok := m["error"]; !ok {
			t.Errorf("%s %s → %d: missing \"error\" key in JSON: %s",
				method, url, status, string(body))
		}
	}
}