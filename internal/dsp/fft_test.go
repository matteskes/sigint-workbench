package dsp

import (
	"math"
	"testing"
)

// makeSine generates a sine wave at the given frequency.
func makeSine(freq, sampleRate, amplitude float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}
	return out
}

func TestComputeFFT_SineAtKnownFreq(t *testing.T) {
	sampleRate := uint32(48000)
	toneFreq := 1000.0
	n := 4096

	samples := makeSine(toneFreq, float64(sampleRate), 1.0, n)
	result, err := ComputeFFT(samples, sampleRate)
	if err != nil {
		t.Fatalf("ComputeFFT returned error: %v", err)
	}
	if result == nil {
		t.Fatal("ComputeFFT returned nil result")
	}
	if len(result.Frequencies) != len(result.Magnitudes) {
		t.Fatalf("Frequencies and Magnitudes length mismatch: %d vs %d",
			len(result.Frequencies), len(result.Magnitudes))
	}

	// Find the bin with maximum power (skip DC bin 0)
	maxIdx := 0
	for i := 1; i < len(result.PowerDB); i++ {
		if result.PowerDB[i] > result.PowerDB[maxIdx] {
			maxIdx = i
		}
	}

	// Expected bin: freq / (sampleRate / nfft) where nfft = 4096 (already power of 2)
	df := float64(sampleRate) / float64(n)
	expectedBin := int(toneFreq / df)

	if maxIdx != expectedBin {
		t.Errorf("peak bin = %d (freq %.1f Hz), expected %d (%.1f Hz)",
			maxIdx, result.Frequencies[maxIdx], expectedBin, toneFreq)
	}

	// The peak power should be significantly above the noise floor
	noiseFloor := DetectNoiseFloor(result.PowerDB)
	if result.PowerDB[maxIdx] < noiseFloor+30 {
		t.Errorf("peak power %.1f dB is not significantly above noise floor %.1f dB",
			result.PowerDB[maxIdx], noiseFloor)
	}

	// SampleRate should be propagated
	if result.SampleRate != sampleRate {
		t.Errorf("SampleRate = %d, expected %d", result.SampleRate, sampleRate)
	}
}

func TestComputeFFT_EmptyInput(t *testing.T) {
	result, err := ComputeFFT(nil, 48000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty input, got %v", result)
	}
}

func TestComputeFFT_NonPowerOfTwo(t *testing.T) {
	// Input length 1000 should be padded to 1024
	samples := makeSine(500, 48000, 0.5, 1000)
	result, err := ComputeFFT(samples, 48000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Output should be nfft/2 = 1024/2 = 512 bins
	if len(result.Frequencies) != 512 {
		t.Errorf("expected 512 frequency bins, got %d", len(result.Frequencies))
	}
}

func TestComputeIQFFT_KnownTone(t *testing.T) {
	sampleRate := uint32(48000)
	toneFreq := 2000.0
	n := 2048

	// Generate complex tone as interleaved I/Q
	iq := make([]float64, n*2)
	for i := 0; i < n; i++ {
		angle := 2 * math.Pi * toneFreq * float64(i) / float64(sampleRate)
		iq[i*2] = math.Cos(angle)   // I
		iq[i*2+1] = math.Sin(angle) // Q
	}

	result, err := ComputeIQFFT(iq, sampleRate)
	if err != nil {
		t.Fatalf("ComputeIQFFT returned error: %v", err)
	}
	if result == nil {
		t.Fatal("ComputeIQFFT returned nil result")
	}

	// Find peak
	maxIdx := 0
	for i := 1; i < len(result.PowerDB); i++ {
		if result.PowerDB[i] > result.PowerDB[maxIdx] {
			maxIdx = i
		}
	}

	// Expected bin
	df := float64(sampleRate) / float64(n)
	expectedBin := int(toneFreq / df)
	if maxIdx != expectedBin {
		t.Errorf("peak bin = %d (freq %.1f Hz), expected %d (%.1f Hz)",
			maxIdx, result.Frequencies[maxIdx], expectedBin, toneFreq)
	}
}

func TestComputeIQFFT_EmptyInput(t *testing.T) {
	result, err := ComputeIQFFT(nil, 48000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty input")
	}
}

func TestComputeIQFFT_NegativeOffset(t *testing.T) {
	// D4 (§5.3): a tone below the tuning center must appear in the wrapped
	// (upper) half with its signed negative offset, and the result must carry
	// the full nfft-bin spectrum plus the bin spacing.
	sampleRate := uint32(4_800_000)
	const n = 4096
	df := float64(sampleRate) / float64(n) // 1000 Hz
	const tone = -100_000.0 // exactly on bin 3996 (nfft - 100)

	iq := make([]float64, n*2)
	for i := 0; i < n; i++ {
		angle := 2 * math.Pi * tone * float64(i) / float64(sampleRate)
		iq[i*2] = math.Cos(angle)
		iq[i*2+1] = math.Sin(angle)
	}

	result, err := ComputeIQFFT(iq, sampleRate)
	if err != nil {
		t.Fatalf("ComputeIQFFT: %v", err)
	}
	if len(result.Frequencies) != n {
		t.Fatalf("Frequencies len = %d, want full wrapped %d", len(result.Frequencies), n)
	}
	if result.BinSpacing != df {
		t.Fatalf("BinSpacing = %v, want %v", result.BinSpacing, df)
	}

	// The negative tone lands in the negative (upper) half; the strongest
	// bin must carry the signed offset -100 kHz.
	maxIdx := 0
	for i := 1; i < len(result.PowerDB); i++ {
		if result.PowerDB[i] > result.PowerDB[maxIdx] {
			maxIdx = i
		}
	}
	if math.Abs(result.Frequencies[maxIdx]-tone) > df {
		t.Fatalf("max bin %d freq = %v, want ~%v", maxIdx, result.Frequencies[maxIdx], tone)
	}
	if result.Frequencies[maxIdx] >= 0 {
		t.Errorf("max bin %d freq = %v, want negative offset", maxIdx, result.Frequencies[maxIdx])
	}

	// PositiveHalf returns the one-sided positive view (train-compatible).
	pos := result.PositiveHalf()
	if len(pos.Frequencies) != n/2 {
		t.Fatalf("PositiveHalf len = %d, want %d", len(pos.Frequencies), n/2)
	}
	for i, f := range pos.Frequencies {
		if f < 0 {
			t.Fatalf("PositiveHalf[%d] = %v, want non-negative", i, f)
		}
	}
}