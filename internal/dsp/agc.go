// Package dsp — automatic gain control.
package dsp

import "math"

// AGC applies automatic gain control to a slice of audio samples.
// It normalizes the signal so the peak is at the target level.
type AGC struct {
	TargetPeak float64 // Target peak amplitude (0.0 to 1.0), default 0.95
	Attack     float64 // Attack time constant (samples), default 1
	Release    float64 // Release time constant (samples), default 50
	gain       float64
}

// NewAGC creates an AGC with default settings.
func NewAGC() *AGC {
	return &AGC{
		TargetPeak: 0.95,
		Attack:     1,
		Release:    50,
		gain:       1.0,
	}
}

// Process applies AGC to the input samples, returning the output.
func (a *AGC) Process(samples []float64) []float64 {
	if len(samples) == 0 {
		return samples
	}

	// Find current peak
	peak := 0.0
	for _, s := range samples {
		if v := math.Abs(s); v > peak {
			peak = v
		}
	}

	if peak == 0 {
		out := make([]float64, len(samples))
		copy(out, samples)
		return out
	}

	// Compute desired gain
	desiredGain := a.TargetPeak / peak

	// Smooth the gain change
	if desiredGain > a.gain {
		// Attacking (reducing gain) — fast
		a.gain += (desiredGain - a.gain) / a.Attack
	} else {
		// Releasing (increasing gain) — slow
		a.gain += (desiredGain - a.gain) / a.Release
	}

	out := make([]float64, len(samples))
	for i, s := range samples {
		v := s * a.gain
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		out[i] = v
	}
	return out
}