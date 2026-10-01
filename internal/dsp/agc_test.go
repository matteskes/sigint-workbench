package dsp

import (
	"math"
	"testing"
)

func TestAGC_NormalizesSmallSignal(t *testing.T) {
	agc := NewAGC()

	// Small amplitude sine (peak = 0.1)
	n := 1000
	samples := make([]float64, n)
	for i := range samples {
		samples[i] = 0.1 * math.Sin(2*math.Pi*1000*float64(i)/48000)
	}

	out := agc.Process(samples)
	if len(out) != n {
		t.Fatalf("output length = %d, expected %d", len(out), n)
	}

	// After AGC, peak should be close to TargetPeak (0.95)
	// (first pass starts with gain=1.0, attack=1, so gain = 0.95/0.1 = 9.5)
	peak := 0.0
	for _, s := range out {
		if v := math.Abs(s); v > peak {
			peak = v
		}
	}
	// Should be at or near the target
	if peak < 0.9 {
		t.Errorf("output peak = %.4f, expected near %.2f", peak, agc.TargetPeak)
	}
	if peak > 1.0 {
		t.Errorf("output peak = %.4f, should not exceed 1.0", peak)
	}
}

func TestAGC_ClampsLargeSignal(t *testing.T) {
	agc := NewAGC()

	// Signal with peak = 2.0 (above 1.0)
	samples := []float64{0.0, 1.0, 2.0, -1.0, 0.0, -0.5, 0.5, 1.5}
	out := agc.Process(samples)

	for i, v := range out {
		if v > 1.0 || v < -1.0 {
			t.Errorf("out[%d] = %f, should be clamped to [-1, 1]", i, v)
		}
	}
}

func TestAGC_SilencePassthrough(t *testing.T) {
	agc := NewAGC()
	samples := []float64{0, 0, 0, 0}
	out := agc.Process(samples)
	for i, v := range out {
		if v != 0 {
			t.Errorf("out[%d] = %f, expected 0", i, v)
		}
	}
}

func TestAGC_EmptyInput(t *testing.T) {
	agc := NewAGC()
	out := agc.Process(nil)
	if out != nil {
		t.Errorf("expected nil output for empty input")
	}
}

func TestAGC_RepeatProcessingConverges(t *testing.T) {
	agc := NewAGC()

	samples := make([]float64, 500)
	for i := range samples {
		samples[i] = 0.05 * math.Sin(2*math.Pi*440*float64(i)/44100)
	}

	// Process repeatedly — gain should converge so peak stabilizes
	var lastPeak float64
	for iter := 0; iter < 20; iter++ {
		out := agc.Process(samples)
		lastPeak = 0
		for _, s := range out {
			if v := math.Abs(s); v > lastPeak {
				lastPeak = v
			}
		}
	}
	// After many passes, peak should be at or very near TargetPeak
	if math.Abs(lastPeak-agc.TargetPeak) > 0.05 {
		t.Errorf("converged peak = %.4f, expected near %.4f", lastPeak, agc.TargetPeak)
	}
}