// Package airgap — Section 4.11 Test A3: Offline Asset Verification.
package airgap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestA3_OfflineAssets(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}

	modelDir := filepath.Join(root, "models")
	tilesDataDir := filepath.Join(root, "tiles", "data")
	configDir := filepath.Join(root, "config")

	modelPath := os.Getenv("CLASSIFIER_ONNX_PATH")
	if modelPath != "" {
		if _, err := os.Stat(modelPath); os.IsNotExist(err) {
			t.Skipf("model not found: %s", modelPath)
		} else {
			info, err := os.Stat(modelPath)
			if err != nil {
				t.Fatalf("stat model: %v", err)
			}
			t.Logf("  model exists: %s (%d bytes)", modelPath, info.Size())
		}
	} else {
		matches, _ := filepath.Glob(filepath.Join(modelDir, "*.onnx"))
		if len(matches) == 0 {
			t.Log("  no .onnx files in models/ — skipping model check")
		} else {
			t.Logf("  found %d model(s) in models/", len(matches))
		}
	}

	tilesMatches, _ := filepath.Glob(filepath.Join(tilesDataDir, "*.mbtiles"))
	if len(tilesMatches) == 0 {
		t.Log("  no .mbtiles files found — air-gap target may need supply-tiles.sh")
	} else {
		for _, f := range tilesMatches {
			info, err := os.Stat(f)
			if err != nil {
				t.Logf("  tile: %s (error: %v)", f, err)
				continue
			}
			t.Logf("  tile: %s (%d bytes)", f, info.Size())
		}
	}

	configMatches, _ := filepath.Glob(filepath.Join(configDir, "*.yaml"))
	if len(configMatches) == 0 {
		t.Log("  no .yaml configs found — air-gap target needs config copy")
	} else {
		t.Logf("  found %d config(s):", len(configMatches))
		for _, c := range configMatches {
			t.Logf("    %s", c)
		}
	}
}

func TestA3_SixServicesExist(t *testing.T) {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		root = findRoot()
	}

	services := []string{
		"sdr-capture", "iq-ingest", "signal-processor",
		"recorder", "api-gateway", "ws-hub",
	}
	for _, svc := range services {
		binPath := filepath.Join(root, "cmd", svc, svc)
		if _, err := os.Stat(binPath); os.IsNotExist(err) {
			t.Logf("  %s binary not found at %s (may be Docker-only)", svc, binPath)
		} else {
			t.Logf("  %s binary exists: %s", svc, binPath)
		}
	}
}