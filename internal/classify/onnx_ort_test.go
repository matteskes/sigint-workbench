//go:build onnx

package classify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests must pass whether or not libonnxruntime is present on
// the host (CI has no ONNX Runtime): every failure path must be a
// clean error, never a crash.

func TestONNXLoadModelMissing(t *testing.T) {
	oc := NewONNXClassifier("/nonexistent/classifier.onnx")
	err := oc.Load()
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("Load = %v, want model-not-found error", err)
	}
	if oc.IsLoaded() {
		t.Error("IsLoaded = true after failed Load")
	}
}

// TestONNXLoadInvalidModel ensures Load fails cleanly when the model
// bytes are not a valid ONNX graph. The exact error depends on
// whether the runtime shared library is available.
func TestONNXLoadInvalidModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.onnx")
	if err := os.WriteFile(path, []byte("definitely not onnx"), 0o644); err != nil {
		t.Fatal(err)
	}
	oc := NewONNXClassifier(path)
	if err := oc.Load(); err == nil {
		t.Fatal("Load = nil, want error for invalid model")
	}
	if oc.IsLoaded() {
		t.Error("IsLoaded = true after failed Load")
	}
	if err := oc.Close(); err != nil {
		t.Fatalf("Close after failed Load = %v", err)
	}
}

func TestONNXClassifyBeforeLoad(t *testing.T) {
	oc := NewONNXClassifier("/nonexistent/model.onnx")
	if _, err := oc.Classify(make([]float32, 134), 100_000_000); err == nil {
		t.Fatal("expected Classify error before Load")
	}
}

func TestModulationLabels(t *testing.T) {
	if len(ModulationClasses) != 5 {
		t.Fatalf("len(ModulationClasses) = %d, want 5", len(ModulationClasses))
	}
	if ClassLabel(-1) != "unknown" || ClassLabel(len(ModulationClasses)) != "unknown" {
		t.Error("ClassLabel out-of-range should return unknown")
	}
	for i, label := range ModulationClasses {
		mod, _ := modulationFromLabel(label)
		if mod == "" {
			t.Errorf("label %q at %d has no modulation mapping", label, i)
		}
	}
}