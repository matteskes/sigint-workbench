// Package dsp — digital filters.
package dsp

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
	sections := Butterworth(Lowpass, cutoffHz, 0, sampleRate, order)
	return FilterBiquads(samples, sections)
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