// Package classify — ONNX Runtime ML classifier.
//
// Loads a pre-trained ONNX model and performs spectral feature classification.
// The model is trained offline (Python/PyTorch) and exported to ONNX format.
package classify

import (
	"fmt"
	"os"
)

// ONNXClassifier performs ML-based signal classification using an ONNX model.
type ONNXClassifier struct {
	modelPath string
	session   interface{} // *onnxruntime.Session — will be typed when goonnx is available
	loaded    bool
}

// NewONNXClassifier creates a classifier that will load the model from modelPath.
func NewONNXClassifier(modelPath string) *ONNXClassifier {
	return &ONNXClassifier{modelPath: modelPath}
}

// Load initializes the ONNX session. Call once at startup.
func (oc *ONNXClassifier) Load() error {
	if _, err := os.Stat(oc.modelPath); err != nil {
		return fmt.Errorf("classify: model not found: %s", oc.modelPath)
	}
	// TODO: Initialize ONNX Runtime session
	// sess, err := onnxruntime.NewSession(oc.modelPath, nil)
	// if err != nil { return err }
	// oc.session = sess
	oc.loaded = true
	return nil
}

// IsLoaded reports whether the model has been loaded.
func (oc *ONNXClassifier) IsLoaded() bool {
	return oc.loaded
}

// Classify runs inference on the given spectral features.
// features is a flattened feature vector (spectral bins, bandwidth, etc.)
func (oc *ONNXClassifier) Classify(features []float32, freqHz uint64) (*Result, error) {
	if !oc.loaded {
		return nil, fmt.Errorf("classify: model not loaded")
	}
	// TODO: Run ONNX inference
	// input := onnxruntime.NewTensorFromFloat32(features)
	// outputs, err := oc.session.Run(input)
	// ...
	return nil, fmt.Errorf("classify: ONNX inference not yet implemented")
}