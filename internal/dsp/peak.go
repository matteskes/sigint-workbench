// Package dsp — peak detection in power spectra.
package dsp

import (
	"sort"
)

// Peak represents a detected spectral peak.
type Peak struct {
	FreqHz    float64 // Center frequency of the peak
	PowerDB   float64 // Power at the peak in dB
	Bandwidth float64 // Estimated bandwidth in Hz (−3 dB)
	Index     int     // Bin index in the FFT result
}

// PeakDetector finds peaks in a power spectrum above a threshold.
type PeakDetector struct {
	ThresholdDB float64 // Minimum power in dB
	// MinSNRDB requires a peak to clear the spectrum's own noise
	// floor (DetectNoiseFloor) by this many dB before it counts.
	// Scale-free, unlike ThresholdDB: published power is
	// uncalibrated dBFS and shifts with RF/IF gain (§5.6). 0
	// disables the gate (B4).
	MinSNRDB   float64
	MinSpacing int // Minimum bins between peaks
	TopN       int // Max number of peaks to return
}

// NewPeakDetector creates a peak detector with default settings.
func NewPeakDetector() *PeakDetector {
	return &PeakDetector{
		ThresholdDB: -60,
		MinSpacing:  10,
		TopN:        20,
	}
}

// Detect finds the strongest spectral peaks above the threshold.
//
// It scans the full (one-sided or wrapped) spectrum, collects every local
// maximum that clears ThresholdDB (and, when MinSNRDB > 0, the spectrum's
// own noise floor by MinSNRDB), then returns up to TopN of them, strongest
// first, with at least MinSpacing bins between any two returned peaks.
// Collecting candidates across the whole band before truncating means a
// strong signal at a high bin offset can never be crowded out by
// weaker noise peaks at low offsets.
func (pd *PeakDetector) Detect(result *FFTResult) []Peak {
	if result == nil || len(result.PowerDB) < 3 {
		return nil
	}

	n := len(result.PowerDB)
	// Bin spacing is fs/nfft. ComputeIQFFT returns the full wrapped spectrum
	// (n == nfft) and ComputeFFT the one-sided view (n == nfft/2); both set
	// BinSpacing, so prefer it. Fall back to the legacy one-sided formula for
	// hand-built test spectra that do not set BinSpacing.
	df := result.BinSpacing
	if df <= 0 {
		df = float64(result.SampleRate) / float64(n*2)
	}

	// B4: the SNR gate. threshold_db gates uncalibrated dBFS, which
	// moves with gain and calibration; a peak that barely rises over
	// the record's own floor is noise regardless of the absolute
	// scale. Floor is computed once per record.
	floor := 0.0
	gate := pd.MinSNRDB > 0
	if gate {
		floor = DetectNoiseFloor(result.PowerDB)
	}

	// Pass 1: every local maximum above the threshold.
	candidates := make([]Peak, 0, 32)
	for i := 1; i < n-1; i++ {
		// Local maximum?
		if result.PowerDB[i] <= result.PowerDB[i-1] || result.PowerDB[i] <= result.PowerDB[i+1] {
			continue
		}
		// Above threshold?
		if result.PowerDB[i] < pd.ThresholdDB {
			continue
		}
		// Above the record's own noise floor?
		if gate && result.PowerDB[i]-floor < pd.MinSNRDB {
			continue
		}
		candidates = append(candidates, Peak{
			FreqHz:    result.Frequencies[i],
			PowerDB:   result.PowerDB[i],
			Bandwidth: pd.estimateBandwidth(result, i, df),
			Index:     i,
		})
	}

	// Pass 2: strongest first, enforcing minimum spacing.
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].PowerDB > candidates[b].PowerDB
	})
	var peaks []Peak
	for _, c := range candidates {
		if len(peaks) >= pd.TopN {
			break
		}
		spaced := true
		for _, p := range peaks {
			if absInt(c.Index-p.Index) < pd.MinSpacing {
				spaced = false
				break
			}
		}
		if spaced {
			peaks = append(peaks, c)
		}
	}

	return peaks
}

// estimateBandwidth finds the −3 dB bandwidth around a peak bin.
// Mirrors estimate_bandwidth_hz in models/train.py exactly (>= compare,
// walk stops one bin short of the edge) so the Go pipeline feeds the
// ONNX classifier the same features it was trained on.
func (pd *PeakDetector) estimateBandwidth(result *FFTResult, center int, df float64) float64 {
	n := len(result.PowerDB)
	if n < 3 || center < 0 || center >= n {
		return 0
	}
	threshold := result.PowerDB[center] - 3.0
	lo := center
	for lo > 0 && result.PowerDB[lo-1] >= threshold {
		lo--
	}
	hi := center
	for hi < n-1 && result.PowerDB[hi+1] >= threshold {
		hi++
	}
	if hi <= lo {
		return 0
	}
	return float64(hi-lo) * df
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// DetectNoiseFloor estimates the noise floor from a power spectrum:
// the median of the lower 50% of bins. Mirrors detect_noise_floor in
// models/train.py (np.sort over the lower half).
func DetectNoiseFloor(powerDB []float64) float64 {
	if len(powerDB) == 0 {
		return 0
	}
	half := len(powerDB) / 2
	sorted := make([]float64, half)
	copy(sorted, powerDB[:half])
	sort.Float64s(sorted) // O(n log n); was an O(n²) selection sort (A15)
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
