package classify

import (
	"math"
	"testing"

	"sigint-workbench/internal/dsp"
)

// synthResult builds a synthetic one-sided spectrum: a tone of powerDB
// dB at bin `toneIdx` on top of a -100 dB noise floor.
func synthResult(n, toneIdx int, toneDB float64) *dsp.FFTResult {
	res := &dsp.FFTResult{
		Frequencies: make([]float64, n),
		Magnitudes:  make([]float64, n),
		PowerDB:     make([]float64, n),
		SampleRate:  24000,
	}
	// 200 Hz per bin
	for i := range res.PowerDB {
		res.Frequencies[i] = float64(i) * 200
		res.PowerDB[i] = -100
		res.Magnitudes[i] = 1e-10
	}
	res.PowerDB[toneIdx] = toneDB
	res.Magnitudes[toneIdx] = math.Pow(10, toneDB/10)
	return res
}

func TestExtractFeaturesToVectorLength(t *testing.T) {
	res := synthResult(64, 20, 0)
	f := ExtractFeatures(res, 146_000_000)
	if f == nil {
		t.Fatal("ExtractFeatures returned nil")
	}
	vec := f.ToVector()
	if len(vec) != 134 { // 6 scalars + 128 spectral bins
		t.Fatalf("vector length = %d, want 134", len(vec))
	}
}

func TestExtractFeaturesNilAndEmpty(t *testing.T) {
	if f := ExtractFeatures(nil, 146_000_000); f != nil {
		t.Fatal("expected nil for nil result")
	}
	if f := ExtractFeatures(&dsp.FFTResult{}, 146_000_000); f != nil {
		t.Fatal("expected nil for empty result")
	}
}

func TestExtractFeaturesBandwidth(t *testing.T) {
	// Tone 15 dB above floor spread over 3 bins (peak 0, neighbors -1 dB).
	res := synthResult(64, 20, 0)
	res.PowerDB[19] = -1
	res.PowerDB[21] = -1
	f := ExtractFeatures(res, 146_000_000)
	// -3 dB edge: neighbors at -1 dB are above -3 dB, so span is bins
	// 19..21 → 2 * 200 Hz.
	if f.BandwidthHz != 400 {
		t.Fatalf("bandwidth = %v Hz, want 400", f.BandwidthHz)
	}
}

func TestExtractFeaturesCrestAndEntropy(t *testing.T) {
	// Single strong tone → low entropy, high crest.
	spike := ExtractFeatures(synthResult(64, 20, 0), 1)

	// Uniform spectrum → entropy ≈ 1, crest ≈ 1.
	uniform := &dsp.FFTResult{
		Frequencies: make([]float64, 64),
		Magnitudes:  make([]float64, 64),
		PowerDB:     make([]float64, 64),
		SampleRate:  24000,
	}
	for i := range uniform.PowerDB {
		uniform.Frequencies[i] = float64(i) * 200
		uniform.PowerDB[i] = 0
		uniform.Magnitudes[i] = 1
	}
	flat := ExtractFeatures(uniform, 1)

	if flat.SpectralEntropy < 0.99 || flat.SpectralEntropy > 1.0001 {
		t.Fatalf("flat entropy = %v, want ~1", flat.SpectralEntropy)
	}
	if math.Abs(flat.CrestFactor-1) > 0.01 {
		t.Fatalf("flat crest = %v, want ~1", flat.CrestFactor)
	}
	if spike.SpectralEntropy >= flat.SpectralEntropy {
		t.Fatalf("spike entropy %v should be < flat entropy %v", spike.SpectralEntropy, flat.SpectralEntropy)
	}
	if spike.CrestFactor <= flat.CrestFactor {
		t.Fatalf("spike crest %v should be > flat crest %v", spike.CrestFactor, flat.CrestFactor)
	}
}

func TestExtractFeaturesSNR(t *testing.T) {
	res := synthResult(64, 20, 20)
	f := ExtractFeatures(res, 146_000_000)
	if math.Abs(f.SNRdB-120) > 1 { // 20 dB tone, -100 dB floor
		t.Fatalf("SNR = %v dB, want ~120", f.SNRdB)
	}
}