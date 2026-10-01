// Package dsp — peak detection in power spectra.
package dsp

import "math"

// Peak represents a detected spectral peak.
type Peak struct {
	FreqHz    float64 // Center frequency of the peak
	PowerDB   float64 // Power at the peak in dB
	Bandwidth float64 // Estimated bandwidth in Hz (−3 dB)
	Index     int     // Bin index in the FFT result
}

// PeakDetector finds peaks in a power spectrum above a threshold.
type PeakDetector struct {
	ThresholdDB float64  // Minimum power in dB
	MinSpacing  int      // Minimum bins between peaks
	TopN        int      // Max number of peaks to return
}

// NewPeakDetector creates a peak detector with default settings.
func NewPeakDetector() *PeakDetector {
	return &PeakDetector{
		ThresholdDB: -60,
		MinSpacing:  10,
		TopN:        20,
	}
}

// Detect finds all peaks in the given FFT result.
func (pd *PeakDetector) Detect(result *FFTResult) []Peak {
	if result == nil || len(result.PowerDB) < 3 {
		return nil
	}

	var peaks []Peak
	n := len(result.PowerDB)
	df := float64(result.SampleRate) / float64(n*2) // one-sided: total bins = n/2, but df = fs/nfft

	for i := 1; i < n-1; i++ {
		// Local maximum?
		if result.PowerDB[i] <= result.PowerDB[i-1] || result.PowerDB[i] <= result.PowerDB[i+1] {
			continue
		}
		// Above threshold?
		if result.PowerDB[i] < pd.ThresholdDB {
			continue
		}

		// Check minimum spacing from already-found peaks
		spaced := true
		for _, p := range peaks {
			if absInt(i-p.Index) < pd.MinSpacing {
				spaced = false
				break
			}
		}
		if !spaced {
			continue
		}

		// Estimate bandwidth (−3 dB)
		bw := pd.estimateBandwidth(result, i, df)

		peaks = append(peaks, Peak{
			FreqHz:    result.Frequencies[i],
			PowerDB:   result.PowerDB[i],
			Bandwidth: bw,
			Index:     i,
		})

		if len(peaks) >= pd.TopN {
			break
		}
	}

	return peaks
}

// estimateBandwidth finds the −3 dB bandwidth around a peak bin.
func (pd *PeakDetector) estimateBandwidth(result *FFTResult, center int, df float64) float64 {
	threshold := result.PowerDB[center] - 3.0
	lo := center
	for lo > 0 && result.PowerDB[lo] > threshold {
		lo--
	}
	hi := center
	for hi < len(result.PowerDB)-1 && result.PowerDB[hi] > threshold {
		hi++
	}
	return float64(hi-lo) * df
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// DetectNoiseFloor estimates the noise floor from a power spectrum.
// Uses the median of the lower 50% of bins.
func DetectNoiseFloor(powerDB []float64) float64 {
	if len(powerDB) == 0 {
		return 0
	}
	half := len(powerDB) / 2
	sorted := make([]float64, half)
	copy(sorted, powerDB[:half])
	// Simple selection sort for median (half is small, ~2048)
	for i := 0; i < len(sorted); i++ {
		minIdx := i
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[minIdx] {
				minIdx = j
			}
		}
		sorted[i], sorted[minIdx] = sorted[minIdx], sorted[i]
	}
	return sorted[len(sorted)/2]
}

// SNR computes signal-to-noise ratio for a peak.
func SNR(peak Power, noiseFloor float64) float64 {
	return peak.PowerDB - noiseFloor
}

// Power is a convenience type for a single power measurement.
type Power struct {
	FreqHz  float64
	PowerDB float64
}

var _ = math.Abs // keep math import