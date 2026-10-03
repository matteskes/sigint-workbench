//go:build onnx

package classify

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigint-workbench/internal/dsp"
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

// TestONNXInferenceEndToEnd runs the real Go pipeline (IQ samples ->
// ComputeIQFFT -> ExtractFeatures -> ONNX) against the trained model.
// Skips unless CLASSIFIER_ONNX_PATH names a model file and the ONNX
// Runtime library is loadable (ORT_LIBRARY_PATH).
func TestONNXInferenceEndToEnd(t *testing.T) {
	modelPath := os.Getenv("CLASSIFIER_ONNX_PATH")
	if modelPath == "" {
		t.Skip("CLASSIFIER_ONNX_PATH not set")
	}
	oc := NewONNXClassifier(modelPath)
	if err := oc.Load(); err != nil {
		t.Skipf("model not loadable (set ORT_LIBRARY_PATH to libonnxruntime?): %v", err)
	}
	defer oc.Close()

	// Contract check: dim 0 of ToVector is log2(freq kHz).
	if f := ExtractFeatures(synthResult(64, 20, 0), 1_500_000); f == nil {
		t.Fatal("ExtractFeatures returned nil")
	} else {
		const want = 10.5512 // log2(1500 kHz)
		if got := f.ToVector()[0]; math.Abs(float64(got)-want) > 0.01 {
			t.Fatalf("ToVector()[0] = %v, want ~%v (log2 kHz contract)", got, want)
		}
	}

	// CW frame: 1.5 MHz offset on a 14.5 MHz frame -> 16 MHz peak, tone
	// 25 dB above a 1e-6-power noise floor (training range is 18-30 dB).
	cwIQ := e2eToneFrame(t, 1.5e6, 0)
	res, err := dsp.ComputeIQFFT(cwIQ, e2eSampleRate)
	if err != nil || res == nil {
		t.Fatalf("ComputeIQFFT(cw): %v", err)
	}
	f := ExtractFeatures(res.PositiveHalf(), 14_500_000)
	if f == nil {
		t.Fatal("ExtractFeatures(cw) = nil")
	}
	got, err := oc.Classify(f.ToVector(), 16_000_000)
	if err != nil {
		t.Fatalf("Classify(cw) = %v", err)
	}
	t.Logf("CW frame  -> %s %s conf=%.3f snr=%.1f bw=%.0f Hz",
		got.Modulation, got.SubType, got.Confidence, f.SNRdB, f.BandwidthHz)
	if got.Method != "onnx" {
		t.Errorf("Method = %q, want onnx", got.Method)
	}
	if got.Modulation != "CW" {
		t.Errorf("CW frame classified %s/%s, want CW", got.Modulation, got.SubType)
	}

	// WFM frame: 800 kHz offset on a 100 MHz frame, Carson width ~140 kHz.
	// (Carrier bin 800 sits inside the training carrier range — train.py
	// _carrier_bin picks bins 250..2047-bw, so a 100 kHz offset would be
	// out of distribution.)
	wfmIQ := e2eToneFrame(t, 800_000, 70_000)
	res, err = dsp.ComputeIQFFT(wfmIQ, e2eSampleRate)
	if err != nil || res == nil {
		t.Fatalf("ComputeIQFFT(wfm): %v", err)
	}
	f = ExtractFeatures(res.PositiveHalf(), 100_000_000)
	if f == nil {
		t.Fatal("ExtractFeatures(wfm) = nil")
	}
	got, err = oc.Classify(f.ToVector(), 100_800_000)
	if err != nil {
		t.Fatalf("Classify(wfm) = %v", err)
	}
	t.Logf("WFM frame -> %s %s conf=%.3f snr=%.1f bw=%.0f Hz",
		got.Modulation, got.SubType, got.Confidence, f.SNRdB, f.BandwidthHz)
	if got.Modulation != "FM" {
		t.Errorf("WFM frame classified %s/%s, want FM", got.Modulation, got.SubType)
	}
	if got.SubType != "WFM" {
		t.Errorf("WFM frame subtype %s, want WFM (conf %.3f)", got.SubType, got.Confidence)
	}
}

const (
	e2eSampleRate   = 4_096_000
	e2eNFFT         = 4096
	// e2eNoiseFloorDB matches models/train.py NOISE_FLOOR_DB: 1e-6
	// per-sample noise power -> floor ~ 10*log10(NFFT * 1e-6).
	e2eNoiseFloorDB = -23.876
	e2eSNR          = 25.0 // inside the 18-30 dB training range
)

// e2eToneFrame builds a 4096-sample complex frame the way
// models/train.py build_dataset does: a tone (CW when devHz == 0, else FM
// with piecewise-constant deviation ±devHz) at offsetHz, scaled so its
// strongest FFT bin sits 25 dB above the training noise floor, plus
// Gaussian noise of per-sample power 1e-6.
func e2eToneFrame(t *testing.T, offsetHz, devHz float64) []float64 {
	t.Helper()
	rng := rand.New(rand.NewSource(1))

	tone := make([]float64, e2eNFFT*2)
	ph, step := 0.0, 2*math.Pi*offsetHz/e2eSampleRate
	const hopLen = e2eNFFT / 16
	for i := 0; i < e2eNFFT; i++ {
		if devHz > 0 && i%hopLen == 0 {
			step = 2 * math.Pi * (offsetHz + (2*rng.Float64()-1)*devHz) / e2eSampleRate
		}
		tone[2*i] = math.Cos(ph)
		tone[2*i+1] = math.Sin(ph)
		ph += step
	}

	// Scale the tone (train.py _snr_scale) so its peak bin lands 25 dB
	// above the noise floor, then add the noise (train.py _base_noise).
	res, err := dsp.ComputeIQFFT(tone, e2eSampleRate)
	if err != nil || res == nil {
		t.Fatalf("tone FFT: %v", err)
	}
	peak := math.Inf(-1)
	for _, p := range res.PowerDB {
		if p > peak {
			peak = p
		}
	}
	target := e2eNoiseFloorDB + e2eSNR
	gain := math.Pow(10, (target-peak)/20)

	const sigma = 7.0711e-4 // sqrt(1e-6/2)
	out := make([]float64, e2eNFFT*2)
	for i := range tone {
		out[i] = gain*tone[i] + sigma*rng.NormFloat64()
	}
	return out
}