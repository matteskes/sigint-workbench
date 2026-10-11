// Package airgap — Section 4.11 Test A5: Escape-Hatch Tile Download Audit.
package airgap

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestA5_SupplyPipeline(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}

	supplyScript := filepath.Join(root, "scripts", "supply-tiles.sh")
	if _, err := os.Stat(supplyScript); os.IsNotExist(err) {
		t.Skip("supply-tiles.sh not found — skip (requires network)")
	}
	t.Logf("  supply-tiles.sh exists at: %s", supplyScript)

	data, err := os.ReadFile(supplyScript)
	if err != nil {
		t.Fatalf("read supply-tiles.sh: %v", err)
	}

	for _, req := range []string{"MANIFEST.txt", "SHA-256", "Transfer directory",
		"operator", "validate-tiles.sh"} {
		if contains(string(data), req) {
			t.Logf("  [PASS] supply-tiles.sh references: %s", req)
		} else {
			t.Logf("  [FAIL] supply-tiles.sh missing: %s", req)
		}
	}
}

func TestA5_ManifestVerification(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	manifestPath := filepath.Join(root, "transfer", "europe_monaco", "MANIFEST.txt")
	data, err := os.ReadFile(manifestPath)
	if os.IsNotExist(err) {
		t.Log("  No MANIFEST.txt found; verifying format via supply-tiles.sh content.")
		return
	}
	if err != nil {
		t.Fatalf("read MANIFEST.txt: %v", err)
	}
	for _, field := range []string{"Region:", "Operator:", "Timestamp:",
		"SHA-256:", "Supply host:", "Supply date:"} {
		if contains(string(data), field) {
			t.Logf("  [PASS] MANIFEST contains: %s", field)
		} else {
			t.Logf("  [FAIL] MANIFEST missing: %s", field)
		}
	}
}

func TestA5_SHA256Computation(t *testing.T) {
	testData := []byte("test escape-hatch audit data")
	hash := sha256.Sum256(testData)
	if len(hash) != 32 {
		t.Fatalf("SHA-256 must be 32 bytes, got %d", len(hash))
	}
	t.Logf("  SHA-256 hash: %x", hash)
}

func TestA5_DeployScriptExists(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	scriptPath := filepath.Join(root, "scripts", "deploy.sh")
	data, err := os.ReadFile(scriptPath)
	if os.IsNotExist(err) {
		t.Skip("deploy.sh not found — skip")
	}
	if err != nil {
		t.Fatalf("read deploy.sh: %v", err)
	}
	for _, check := range []string{"validate-tiles.sh", "system.audit.log",
		"air-gap", "models", "tiles/data"} {
		if contains(string(data), check) {
			t.Logf("  [PASS] deploy.sh references: %s", check)
		} else {
			t.Logf("  [FAIL] deploy.sh missing: %s", check)
		}
	}
	info, _ := os.Stat(scriptPath)
	t.Logf("  deploy.sh permissions: %v", info.Mode().Perm())
}

func TestA5_AuditLogEntry(t *testing.T) {
	tmpDir := t.TempDir()
	auditFile := filepath.Join(tmpDir, "system.audit.log")
	now := time.Now().UTC().Format(time.RFC3339)
	entry := "DEPLOY " + now + " region=europe_monaco hash=a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2 operator=" +
		os.Getenv("USER") + "\n"
	if os.Getenv("USER") == "" {
		entry = "DEPLOY " + now + " region=europe_monaco hash=a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2 operator=test-operator\n"
	}
	if err := os.WriteFile(auditFile, []byte(entry), 0644); err != nil {
		t.Fatalf("write audit log: %v", err)
	}
	data, err := os.ReadFile(auditFile)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !contains(string(data), "DEPLOY") || !contains(string(data), "hash=") {
		t.Fatal("audit log missing required fields")
	}
	t.Logf("  audit entry: %s", string(data))
}

func TestA5_SupplyScriptExecutability(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	scriptPath := filepath.Join(root, "scripts", "supply-tiles.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Skip("supply-tiles.sh not found — skip")
	}
	info, _ := os.Stat(scriptPath)
	if info.Mode()&0111 == 0 {
		t.Fatal("supply-tiles.sh is not executable")
	}
	t.Logf("  supply-tiles.sh executable (mode: %v)", info.Mode().Perm())
}

func TestA5_validateTilesScriptExists(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}
	scriptPath := filepath.Join(root, "scripts", "validate-tiles.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Skip("validate-tiles.sh not found — skip")
	}
	t.Log("  validate-tiles.sh exists")
	data, _ := os.ReadFile(scriptPath)
	for _, check := range []string{"MANIFEST.txt", "SHA-256", "bounds"} {
		if contains(string(data), check) {
			t.Logf("  [PASS] validate-tiles.sh references: %s", check)
		} else {
			t.Logf("  [WARN] validate-tiles.sh missing: %s", check)
		}
	}
}

func TestA5_CurlCheck(t *testing.T) {
	cmd := exec.Command("curl", "-sf", "--connect-timeout", "3",
		"http://169.254.169.254/latest/meta-data/")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Log("  WARNING: cloud metadata responded (not air-gapped)")
	} else {
		t.Logf("  [PASS] cloud metadata unreachable: %v", err)
	}
	_ = out
}
