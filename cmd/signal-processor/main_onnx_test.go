package main

import (
	"math"
	"math/rand"
	"os"
	"testing"
	"time"

	"sigint-workbench/internal/classify"
	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
)

// synthetic frames: int16 IQ -> decode -> FFT -> peak detect -> features ->
// ONNX (with rules fallback). Skips unless CLASSIFIER_ONNX_PATH names a
// model file and the ONNX Runtime library is loadable (ORT_LIBRARY_PATH);
// in builds without the onnx tag, Load fails and the test skips too.
func TestProcessFrameONNX(t *testing.T) {
	modelPath := os.Getenv("CLASSIFIER_ONNX_PATH")
	if modelPath == "" {
		t.Skip("CLASSIFIER_ONNX_PATH not set")
	}
	oc := classify.NewONNXClassifier(modelPath)
	if err := oc.Load(); err != nil {
		t.Skipf("model not loadable (set ORT_LIBRARY_PATH to libonnxruntime?): %v", err)
	}
	defer oc.Close()

	clf := &onnxFrameClassifier{onnx: oc, rules: classify.NewRuleClassifier()}
	pd := &dsp.PeakDetector{ThresholdDB: -60, MinSpacing: 10, TopN: 20}

	// CW: 1.5 MHz offset on a 14.5 MHz frame -> 16 MHz peak. (Bin 1500:
	// well past the first 20 low-bin noise peaks, so this also guards the
	// PeakDetector against crowding a strong high-bin signal out of TopN.)
	events := processFrame(pipelineFrame(t, 14_500_000, 1.5e6, 0), pd, clf, dsp.WindowRectangular)
	if len(events) == 0 {
		t.Fatal("no events from CW frame")
	}
	ev := events[0]
	t.Logf("CW  -> peak %.4f MHz %s/%s conf=%.2f bw=%.0f kHz",
		float64(ev.PeakHz)/1e6, ev.Class.Modulation, ev.Class.SubType, ev.Class.Confidence, ev.Class.Bandwidth/1000)
	if ev.Class == nil || ev.Class.Method != "onnx" {
		t.Fatalf("expected onnx result, got %+v", ev.Class)
	}
	if ev.Class.Modulation != "CW" {
		t.Errorf("CW frame classified %s/%s, want CW", ev.Class.Modulation, ev.Class.SubType)
	}
	if got, want := int64(ev.PeakHz), int64(16_000_000); math.Abs(float64(got-want)) > 3000 {
		t.Errorf("peak = %d Hz, want ~%d Hz", got, want)
	}

	// WFM: 800 kHz offset on a 100 MHz frame, Carson width ~140 kHz.
	// Carrier bin 800 is inside the training carrier range (train.py
	// picks bins 250..2047-bw); 100.8 MHz is in the WFM band 88-108 MHz.
	events = processFrame(pipelineFrame(t, 100_000_000, 800_000, 70_000), pd, clf, dsp.WindowRectangular)
	if len(events) == 0 {
		t.Fatal("no events from WFM frame")
	}
	ev = events[0]
	t.Logf("WFM -> peak %.4f MHz %s/%s conf=%.2f bw=%.0f kHz",
		float64(ev.PeakHz)/1e6, ev.Class.Modulation, ev.Class.SubType, ev.Class.Confidence, ev.Class.Bandwidth/1000)
	if ev.Class == nil || ev.Class.Method != "onnx" {
		t.Fatalf("expected onnx result, got %+v", ev.Class)
	}
	if ev.Class.Modulation != "FM" {
		t.Errorf("WFM frame classified %s/%s, want FM", ev.Class.Modulation, ev.Class.SubType)
	}
	if ev.Class.SubType != "WFM" {
		t.Errorf("WFM frame subtype %s, want WFM (conf %.2f)", ev.Class.SubType, ev.Class.Confidence)
	}
}

// pipelineFrame builds a synthetic frame the way iq-ingest sends it:
// 4096 int16 IQ pairs = tone (CW or FM) + 1e-6-power Gaussian noise,
// scaled exactly like models/train.py build_dataset (tone peak 25 dB
// above the noise floor, then noise added unscaled).
func pipelineFrame(t *testing.T, centerHz uint64, offsetHz, devHz float64) *sdr.IQFrame {
	t.Helper()
	const (
		nfft = 4096
		rate = 4_096_000
	)
	// Per I/Q sample noise sigma; total per-sample power 1e-6 (train.py
	// _base_noise).
	const sigma = 7.0711e-4 // sqrt(1e-6/2)
	rng := rand.New(rand.NewSource(1))
	ph, step := 0.0, 2*math.Pi*offsetHz/rate
	const hopLen = nfft / 16
	tone := make([]float64, nfft*2)
	for i := 0; i < nfft; i++ {
		if devHz > 0 && i%hopLen == 0 {
			step = 2 * math.Pi * (offsetHz + (2*rng.Float64()-1)*devHz) / rate
		}
		tone[2*i] = math.Cos(ph)
		tone[2*i+1] = math.Sin(ph)
		ph += step
	}

	// Scale the tone so its strongest bin is 25 dB above the noise floor.
	res, err := dsp.ComputeIQFFT(tone, rate)
	if err != nil || res == nil {
		t.Fatalf("tone FFT: %v", err)
	}
	peak := math.Inf(-1)
	for _, p := range res.PowerDB {
		if p > peak {
			peak = p
		}
	}
	target := 10*math.Log10(4096*1e-6) + 25
	gain := math.Pow(10, (target-peak)/20)

	samples := make([]int16, nfft*2)
	for i := range tone {
		v := gain*tone[i] + sigma*rng.NormFloat64()
		if v > 1 {
			v = 1
		}
		if v < -1 {
			v = -1
		}
		samples[i] = int16(math.Round(v * 32767))
	}
	return &sdr.IQFrame{
		SDRID:      "e2e-test",
		FreqHz:     centerHz,
		SampleRate: rate,
		Timestamp:  time.Now(),
		Samples:    samples,
	}
}