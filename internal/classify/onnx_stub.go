//go:build !onnx

// Package classify — ONNX classifier stub (default build).
//
// Without the `onnx` build tag the classifier cannot run inference;
// signal-processor falls back to the rule classifier. The stub
// keeps the same exported surface so the rest of the codebase
// builds identically in both modes.
package classify

import (
	"fmt"
	"os"
)

// ONNXClassifier stub — no inference available in this build.
type ONNXClassifier struct {
	modelPath string
	loaded    bool
}

// NewONNXClassifier creates a classifier for the ONNX model at
// modelPath. Load must be called before Classify.
func NewONNXClassifier(modelPath string) *ONNXClassifier {
	return &ONNXClassifier{modelPath: modelPath}
}

// Load checks that the model file exists, then fails with a clear
// message: this binary was built without the onnx build tag.
func (oc *ONNXClassifier) Load() error {
	if _, err := os.Stat(oc.modelPath); err != nil {
		return fmt.Errorf("classify: model not found: %s", oc.modelPath)
	}
	return fmt.Errorf("classify: binary built without onnx build tag; rebuild with -tags onnx for ML inference")
}

// IsLoaded reports whether Load succeeded (always false in stub builds).
func (oc *ONNXClassifier) IsLoaded() bool {
	return oc.loaded
}

// Classify is never available in stub builds.
func (oc *ONNXClassifier) Classify(features []float32, freqHz uint64) (*Result, error) {
	if !oc.loaded {
		return nil, fmt.Errorf("classify: model not loaded")
	}
	return nil, fmt.Errorf("classify: binary built without onnx build tag")
}

// Close releases resources (none in stub builds).
func (oc *ONNXClassifier) Close() error {
	oc.loaded = false
	return nil
}