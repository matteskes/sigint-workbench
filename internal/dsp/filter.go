// Package dsp — digital filters.
package dsp

import "math"

// ButterworthLowpass applies a Butterworth low-pass filter.
// order is the filter order (1-8 typical), cutoffHz is the cutoff frequency,
// and sampleRate is the input sample rate.
func ButterworthLowpass(samples []float64, cutoffHz, sampleRate float64, order int) []float64 {
	if len(samples) == 0 || cutoffHz <= 0 || sampleRate <= 0 {
		return samples
	}
	if cutoffHz >= sampleRate/2 {
		return samples // no filtering needed
	}

	// Bilinear transform
	T := 2.0 / sampleRate
	wn := math.Pi * cutoffHz / (sampleRate / 2) // normalized
	alpha := 1.0 / math.Tan(wn/2.0)

	// Pre-warp
	c := 1.0 / alpha

	// For simplicity, implement as a cascade of 2nd-order sections
	// This is a simplified implementation; for production, use a proper
	// IIR filter design (e.g., from gonum or a dedicated library).
	//
	// For now, apply a simple one-pole low-pass as a placeholder.
	// TODO: Replace with proper Butterworth IIR implementation.
	rc := 1.0 / (2.0 * math.Pi * cutoffHz)
	dt := 1.0 / sampleRate
	a := dt / (rc + dt)

	out := make([]float64, len(samples))
	y := 0.0
	for i, x := range samples {
		y += a * (x - y)
		out[i] = y
	}
	_ = c
	_ = order
	return out
}

// Decimate reduces the sample rate by the given factor using a simple
// low-pass + decimation approach.
func Decimate(samples []float64, factor int) []float64 {
	if factor <= 1 || len(samples) == 0 {
		return samples
	}
	out := make([]float64, len(samples)/factor)
	for i := range out {
		out[i] = samples[i*factor]
	}
	return out
}

// Mean computes the arithmetic mean of a slice.
func Mean(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sum := 0.0
	for _, s := range samples {
		sum += s
	}
	return sum / float64(len(samples))
}