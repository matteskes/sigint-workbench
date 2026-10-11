// Package airgap — Section 4.11 Air-Gap & Security Hardening shared helpers.
package airgap

import (
	"os"
	"path/filepath"
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

// contains checks whether s contains substr.
func contains(s, substr string) bool {
	if len(substr) == 0 || len(s) == 0 {
		return len(substr) == 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
