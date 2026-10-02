// Package classify — spectral feature extraction for ML classification.
package classify

import (
	"math"

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

	bandwidth := estimateBandwidthHz(result, peakIdx)
	crest := crestFactor(result.PowerDB)
	entropy := spectralEntropy(result.PowerDB)

	// Normalize spectrum to fixed length
	spectrum := normalizeSpectrum(result.PowerDB, noiseFloor, FeatureVectorLength)

	return &SpectralFeatures{
		FreqHz:          float64(centerFreqHz),
		BandwidthHz:     bandwidth,
		PeakPowerDB:     peakPower,
		NoiseFloorDB:    noiseFloor,
		SNRdB:           snr,
		SpectralShape:   spectrum,
		CrestFactor:     crest,
		SpectralEntropy: entropy,
	}
}

// estimateBandwidthHz returns the -3 dB bandwidth in Hz measured around
// the peak bin.
func estimateBandwidthHz(result *dsp.FFTResult, peakIdx int) float64 {
	pow := result.PowerDB
	n := len(pow)
	if n < 3 || peakIdx < 0 || peakIdx >= n {
		return 0
	}
	threshold := pow[peakIdx] - 3.0
	lo, hi := peakIdx, peakIdx
	for lo > 0 && pow[lo-1] >= threshold {
		lo--
	}
	for hi < n-1 && pow[hi+1] >= threshold {
		hi++
	}
	if hi <= lo || len(result.Frequencies) <= hi {
		return 0
	}
	return result.Frequencies[hi] - result.Frequencies[lo]
}

// crestFactor computes the peak-to-RMS ratio of the linear power
// spectrum.
func crestFactor(powerDB []float64) float64 {
	peak := 0.0
	sumSq := 0.0
	for _, p := range powerDB {
		lin := math.Pow(10, p/10)
		if lin > peak {
			peak = lin
		}
		sumSq += lin * lin
	}
	rms := math.Sqrt(sumSq / float64(len(powerDB)))
	if rms <= 0 {
		return 0
	}
	return peak / rms
}

// spectralEntropy returns the Shannon entropy of the normalized linear
// power spectrum, scaled to [0, 1] (1 = uniform, 0 = single bin).
func spectralEntropy(powerDB []float64) float64 {
	total := 0.0
	lins := make([]float64, len(powerDB))
	for i, p := range powerDB {
		lins[i] = math.Pow(10, p/10)
		total += lins[i]
	}
	if total <= 0 {
		return 0
	}
	h := 0.0
	for _, lin := range lins {
		if lin <= 0 {
			continue
		}
		p := lin / total
		h -= p * math.Log2(p)
	}
	maxH := math.Log2(float64(len(powerDB)))
	if maxH <= 0 {
		return 0
	}
	return h / maxH
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