package dsp

import (
	"math"
	"testing"
)

func TestButterworthLowpass_AttenuatesHighFreq(t *testing.T) {
	sampleRate := 48000.0
	cutoff := 5000.0
	n := 4800 // 100 ms

	// Mix of 1 kHz (below cutoff) and 10 kHz (above cutoff)
	samples := make([]float64, n)
	for i := range samples {
		t := float64(i) / sampleRate
		samples[i] = 1.0*math.Sin(2*math.Pi*1000*t) + 1.0*math.Sin(2*math.Pi*10000*t)
	}

	out := ButterworthLowpass(samples, cutoff, sampleRate, 4)
	if len(out) != n {
		t.Fatalf("output length = %d, expected %d", len(out), n)
	}

	// Compute RMS of input and output — output should have less energy
	// since the high-freq component is attenuated
	inRMS, outRMS := 0.0, 0.0
	for i := range samples {
		inRMS += samples[i] * samples[i]
		outRMS += out[i] * out[i]
	}
	inRMS = math.Sqrt(inRMS / float64(n))
	outRMS = math.Sqrt(outRMS / float64(n))

	if outRMS >= inRMS {
		t.Errorf("output RMS (%.4f) should be less than input RMS (%.4f) after LPF", outRMS, inRMS)
	}
}

func TestButterworthLowpass_NoFilterNeeded(t *testing.T) {
	samples := []float64{1, 2, 3, 4}

	// Cutoff >= Nyquist → return unchanged
	out := ButterworthLowpass(samples, 24000, 48000, 4)
	for i := range samples {
		if out[i] != samples[i] {
			t.Errorf("out[%d] = %f, expected %f (no filter)", i, out[i], samples[i])
		}
	}
}

func TestButterworthLowpass_EmptyInput(t *testing.T) {
	out := ButterworthLowpass(nil, 1000, 48000, 4)
	if out != nil {
		t.Errorf("expected nil for empty input")
	}
}

func TestButterworthLowpass_InvalidParams(t *testing.T) {
	samples := []float64{1, 2, 3}

	// Zero cutoff
	out := ButterworthLowpass(samples, 0, 48000, 4)
	if len(out) != len(samples) {
		t.Errorf("should return input unchanged for cutoff=0")
	}

	// Zero sample rate
	out = ButterworthLowpass(samples, 1000, 0, 4)
	if len(out) != len(samples) {
		t.Errorf("should return input unchanged for sampleRate=0")
	}
}

func TestDecimate(t *testing.T) {
	input := []float64{10, 20, 30, 40, 50, 60, 70, 80}

	out := Decimate(input, 2)
	if len(out) != 4 {
		t.Fatalf("Decimate(2) length = %d, expected 4", len(out))
	}
	expected := []float64{10, 30, 50, 70}
	for i, v := range out {
		if v != expected[i] {
			t.Errorf("out[%d] = %f, expected %f", i, v, expected[i])
		}
	}

	// Factor 1 → no change
	out = Decimate(input, 1)
	if len(out) != len(input) {
		t.Errorf("Decimate(1) length = %d, expected %d", len(out), len(input))
	}

	// Factor > len → empty
	out = Decimate(input, 100)
	if len(out) != 0 {
		t.Errorf("Decimate(100) length = %d, expected 0", len(out))
	}
}

func TestDecimate_InvalidFactor(t *testing.T) {
	input := []float64{1, 2, 3}
	out := Decimate(input, 0)
	if len(out) != len(input) {
		t.Errorf("Decimate(0) should return input unchanged, got length %d", len(out))
	}
}

func TestMean(t *testing.T) {
	if v := Mean([]float64{1, 2, 3, 4}); v != 2.5 {
		t.Errorf("Mean([1,2,3,4]) = %f, expected 2.5", v)
	}
	if v := Mean([]float64{-1, 1}); v != 0 {
		t.Errorf("Mean([-1,1]) = %f, expected 0", v)
	}
	if v := Mean(nil); v != 0 {
		t.Errorf("Mean(nil) = %f, expected 0", v)
	}
	if v := Mean([]float64{42}); v != 42 {
		t.Errorf("Mean([42]) = %f, expected 42", v)
	}
}