// Package main — pure calibration math for rtl-calibrate. Split from
// main.go so it stays unit-testable without hardware or build tags
// (the measurement path goes through internal/sdr, whose stub errors
// when built without the rtlsdr tag).
package main

import "math"

// PeakPowerDB finds the strongest bin in a wrapped power spectrum,
// skipping bins whose baseband offset lies within dcGuardHz of 0 Hz —
// the RTL2832U places a DC-carrier spike at baseband 0 that would
// otherwise dominate the search. freqHz holds the per-bin baseband
// offsets as returned by dsp.ComputeIQFFT (signed, wrapped). ok is
// false when no bin remains (empty spectrum, or everything inside the
// guard band).
func PeakPowerDB(powerDB, freqHz []float64, dcGuardHz float64) (peakDB float64, peakBin int, ok bool) {
	best := math.Inf(-1)
	bestBin := -1
	for i, p := range powerDB {
		if dcGuardHz > 0 && i < len(freqHz) && math.Abs(freqHz[i]) < dcGuardHz {
			continue
		}
		if p > best {
			best = p
			bestBin = i
		}
	}
	if bestBin < 0 {
		return 0, -1, false
	}
	return best, bestBin, true
}

// ImpliedOffset inverts the §5.6 transform for one reference
// measurement:
//
//	offset = expected_dbm − mean_power_db + gain_db
//
// so that power_db − gain + offset reproduces expected_dbm. gain_db is
// the APPLIED tuner gain the driver reports (AppliedGainDB, §15.3
// defect 1) — the figure sdr-capture serves on its §7.4 status
// endpoint and signal-processor uses as applied_gain_db. The requested
// value may snap to a different tuner step, so the caller must pass
// the applied figure; passing the requested one instead shifts the
// offset by that step delta (docs/HARDWARE.md §6.3).
func ImpliedOffset(expectedDBM, meanPowerDB, gainDB float64) float64 {
	return expectedDBM - meanPowerDB + gainDB
}

// Mean returns the arithmetic mean; 0 for an empty slice.
func Mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// StdDev returns the population standard deviation; 0 for slices with
// fewer than two samples.
func StdDev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := Mean(xs)
	ss := 0.0
	for _, x := range xs {
		ss += (x - m) * (x - m)
	}
	return math.Sqrt(ss / float64(len(xs)))
}
