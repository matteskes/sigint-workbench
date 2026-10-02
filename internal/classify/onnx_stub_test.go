//go:build !onnx

package classify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stub build must expose the same interface and fail with clear
// errors instead of silently "classifying".

func TestONNXStubLoadModelMissing(t *testing.T) {
	oc := NewONNXClassifier("/nonexistent/classifier.onnx")
	err := oc.Load()
	if err == nil {
		t.Fatal("expected error for missing model file")
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("error = %q, want mention of missing model", err)
	}
	if oc.IsLoaded() {
		t.Error("IsLoaded = true after failed Load")
	}
}

func TestONNXStubLoadTagError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.onnx")
	if err := os.WriteFile(path, []byte("not a real onnx model"), 0o644); err != nil {
		t.Fatal(err)
	}
	oc := NewONNXClassifier(path)
	err := oc.Load()
	if err == nil {
		t.Fatal("expected error: stub build cannot do ONNX inference")
	}
	if !strings.Contains(err.Error(), "onnx build tag") {
		t.Fatalf("error = %q, want mention of missing build tag", err)
	}
	if oc.IsLoaded() {
		t.Error("IsLoaded = true in stub build")
	}
}

func TestONNXStubClassify(t *testing.T) {
	oc := NewONNXClassifier("/nonexistent/model.onnx")
	if _, err := oc.Classify(make([]float32, 134), 100_000_000); err == nil {
		t.Fatal("expected Classify error when model not loaded")
	}
	// A loaded stub (impossible in this build) would still refuse to
	// infer — guard the branch directly.
	oc.loaded = true
	if _, err := oc.Classify(make([]float32, 134), 100_000_000); err == nil {
		t.Fatal("expected Classify error in stub build")
	}
}