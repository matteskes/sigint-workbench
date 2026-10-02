//go:build onnx

// Package classify — ONNX classifier (build with `-tags onnx`).
//
// Wraps the ONNX Runtime (github.com/yalue/onnxruntime_go) via dlopen.
// The runtime shared library is loaded at runtime — it is NOT a link
// dependency — so the tagged build compiles on any cgo-capable host
// (CI, macOS, Linux) even when libonnxruntime is not installed. At
// runtime the library is resolved through ORT_LIBRARY_PATH (or
// "onnxruntime.so" via the dynamic loader's default search path);
// Load returns a clean error when it is absent instead of crashing.
package classify

import (
	"fmt"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// modelIOTensorNames are the ONNX input/output tensor names that
// models/train.py must export with.
const (
	inputTensorName  = "features"
	outputTensorName = "classification"
)

// featureDim is the model input width: 6 scalars + 128 spectral bins.
const featureDim = 134

// ONNXClassifier runs the 134→5 modulation model with ONNX Runtime.
// One classifier instance is safe for concurrent use.
type ONNXClassifier struct {
	mu        sync.Mutex
	modelPath string
	session   *ort.DynamicAdvancedSession
	loaded    bool
}

// NewONNXClassifier creates a classifier for the ONNX model at
// modelPath. Load must be called before Classify.
func NewONNXClassifier(modelPath string) *ONNXClassifier {
	return &ONNXClassifier{modelPath: modelPath}
}

// Load initializes the ONNX Runtime environment and creates the
// session. It fails cleanly when the model file or the runtime
// shared library is missing.
func (oc *ONNXClassifier) Load() error {
	if _, err := os.Stat(oc.modelPath); err != nil {
		return fmt.Errorf("classify: model not found: %s", oc.modelPath)
	}
	if lib := os.Getenv("ORT_LIBRARY_PATH"); lib != "" {
		ort.SetSharedLibraryPath(lib)
	}
	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(ort.WithLogLevelWarning()); err != nil {
			return fmt.Errorf("classify: initialize onnxruntime: %w", err)
		}
	}
	sess, err := ort.NewDynamicAdvancedSession(
		oc.modelPath,
		[]string{inputTensorName},
		[]string{outputTensorName},
		nil,
	)
	if err != nil {
		return fmt.Errorf("classify: create onnx session: %w", err)
	}
	oc.session = sess
	oc.loaded = true
	return nil
}

// IsLoaded reports whether Load succeeded.
func (oc *ONNXClassifier) IsLoaded() bool {
	oc.mu.Lock()
	defer oc.mu.Unlock()
	return oc.loaded
}

// Classify runs inference on a 134-dim feature vector (see
// features.ToVector) and returns the highest-probability class.
// Confidence is the model's probability for the winning class.
func (oc *ONNXClassifier) Classify(features []float32, freqHz uint64) (*Result, error) {
	oc.mu.Lock()
	defer oc.mu.Unlock()
	if !oc.loaded || oc.session == nil {
		return nil, fmt.Errorf("classify: model not loaded")
	}
	if len(features) != featureDim {
		return nil, fmt.Errorf("classify: feature vector length = %d, want %d", len(features), featureDim)
	}

	input, err := ort.NewTensor(ort.NewShape(1, featureDim), features)
	if err != nil {
		return nil, fmt.Errorf("classify: build input tensor: %w", err)
	}
	defer input.Destroy()

	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(len(ModulationClasses))))
	if err != nil {
		return nil, fmt.Errorf("classify: build output tensor: %w", err)
	}
	defer output.Destroy()

	if err := oc.session.Run([]ort.Value{input}, []ort.Value{output}); err != nil {
		return nil, fmt.Errorf("classify: run inference: %w", err)
	}

	probs := output.GetData()
	if len(probs) != len(ModulationClasses) {
		return nil, fmt.Errorf("classify: output length = %d, want %d", len(probs), len(ModulationClasses))
	}

	best, bestP := 0, float32(-1)
	for i, p := range probs {
		if p > bestP {
			best, bestP = i, p
		}
	}
	if bestP < 0 {
		bestP = 0
	}

	label := ClassLabel(best)
	modulation, subType := modulationFromLabel(label)
	return &Result{
		Modulation: modulation,
		SubType:    subType,
		Source:     "onnx:" + label,
		Confidence: float64(bestP),
		Method:     "onnx",
		Frequency:  freqHz,
	}, nil
}

// Close destroys the ONNX Runtime session.
func (oc *ONNXClassifier) Close() error {
	oc.mu.Lock()
	defer oc.mu.Unlock()
	if oc.session != nil {
		err := oc.session.Destroy()
		oc.session = nil
		oc.loaded = false
		return err
	}
	return nil
}