// Package classify — spectral feature extraction for ML classification.
package classify

import (
	"sigint-workbench/internal/dsp"
)

// SpectralFeatures extracts a feature vector from an FFT result
// suitable for ML classification.
type SpectralFeatures struct {
	FreqHz        float64  // Center frequency
	BandwidthHz   float64  // Estimated bandwidth
	PeakPowerDB   float64  // Peak power
	NoiseFloorDB  float64  // Noise floor
	SNRdB         float64  // Signal-to-noise ratio
	SpectralShape []float32 // Normalized spectrum (fixed length)
	CrestFactor   float64  // Peak / RMS ratio
	SpectralEntropy float64 // Shannon entropy of normalized spectrum
}

// FeatureVectorLength is the fixed length of the spectral shape feature.
const FeatureVectorLength = 128

// ExtractFeatures computes the feature vector from an FFT result.
func ExtractFeatures(result *dsp.FFTResult, centerFreqHz uint64) *SpectralFeatures {
	if result == nil || len(result.PowerDB) == 0 {
		return nil
	}

	noiseFloor := dsp.DetectNoiseFloor(result.PowerDB)

	// Find peak
	peakIdx := 0
	for i, p := range result.PowerDB {
		if p > result.PowerDB[peakIdx] {
			peakIdx = i
		}
	}
	peakPower := result.PowerDB[peakIdx]
	snr := peakPower - noiseFloor

	// Estimate bandwidth
	peak := dsp.Peak{
		FreqHz:  result.Frequencies[peakIdx],
		PowerDB: peakPower,
		Index:   peakIdx,
	}
	_ = peak

	// Normalize spectrum to fixed length
	spectrum := normalizeSpectrum(result.PowerDB, noiseFloor, FeatureVectorLength)

	// Crest factor
	rms := 0.0
	for _, p := range result.PowerDB {
		lin := 10.0 / (p / 10.0) // approximate
		rms += lin * lin
	}
	rms = rms / float64(len(result.PowerDB))
	rms = 0.1 // placeholder until proper computation
	crest := 0.0
	if rms > 0 {
		crest = (10.0 / (peakPower / 10.0)) / rms
	}

	// Spectral entropy
	entropy := 0.0
	for _, p := range spectrum {
		if p > 0 {
			entropy -= float64(p) * float64(64.0) * (float32(1.0) / float32(64.0)) // simplified
		}
	}
	_ = entropy

	return &SpectralFeatures{
		FreqHz:          float64(centerFreqHz),
		BandwidthHz:     0, // TODO: proper bandwidth estimation
		PeakPowerDB:     peakPower,
		NoiseFloorDB:    noiseFloor,
		SNRdB:           snr,
		SpectralShape:   spectrum,
		CrestFactor:     crest,
		SpectralEntropy: 0, // TODO
	}
}

// ToVector flattens the features into a single float32 slice for ONNX input.
func (sf *SpectralFeatures) ToVector() []float32 {
	vec := make([]float32, 0, FeatureVectorLength+6)
	vec = append(vec, float32(sf.FreqHz))
	vec = append(vec, float32(sf.BandwidthHz))
	vec = append(vec, float32(sf.PeakPowerDB))
	vec = append(vec, float32(sf.NoiseFloorDB))
	vec = append(vec, float32(sf.SNRdB))
	vec = append(vec, float32(sf.CrestFactor))
	vec = append(vec, sf.SpectralShape...)
	return vec
}

// normalizeSpectrum resamples the power spectrum to a fixed length
// and normalizes to [0, 1].
func normalizeSpectrum(powerDB []float64, noiseFloor float64, targetLen int) []float32 {
	if len(powerDB) == 0 {
		return make([]float32, targetLen)
	}

	out := make([]float32, targetLen)
	srcLen := len(powerDB)
	for i := 0; i < targetLen; i++ {
		srcIdx := i * srcLen / targetLen
		if srcIdx >= srcLen {
			srcIdx = srcLen - 1
		}
		v := powerDB[srcIdx] - noiseFloor
		if v < 0 {
			v = 0
		}
		if v > 60 {
			v = 60
		}
		out[i] = float32(v) / 60.0
	}
	return out
}