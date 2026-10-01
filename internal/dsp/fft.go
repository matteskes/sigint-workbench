// Package dsp provides digital signal processing primitives.
package dsp

import (
	"gonum.org/v1/gonum/dsp/fourier"
)

// FFTResult holds the output of an FFT computation.
type FFTResult struct {
	Frequencies []float64 // Hz, length = N/2 (one-sided)
	Magnitudes  []float64 // linear magnitude, length = N/2
	PowerDB     []float64 // power in dB, length = N/2
	SampleRate  uint32
}

// ComputeFFT performs a real-input FFT on the given time-domain samples.
// The input is treated as a single channel (I or Q, or magnitude).
// Returns a one-sided spectrum (N/2 bins).
func ComputeFFT(samples []float64, sampleRate uint32) (*FFTResult, error) {
	n := len(samples)
	if n == 0 {
		return nil, nil
	}

	// gonum requires power-of-2 length for FFT; pad with zeros if needed
	nfft := 1
	for nfft < n {
		nfft <<= 1
	}

	// Allocate complex buffer
	re := make([]float64, nfft)
	im := make([]float64, nfft)
	copy(re, samples)
	// im is already zeroed

	// Perform FFT
	fourier.FFT(true, re, im) // forward transform in-place

	// Compute one-sided spectrum
	half := nfft / 2
	freqs := make([]float64, half)
	mags := make([]float64, half)
	powerDB := make([]float64, half)
	df := float64(sampleRate) / float64(nfft)

	for i := 0; i < half; i++ {
		freqs[i] = float64(i) * df
		mag := (re[i]*re[i] + im[i]*im[i])
		mags[i] = mag
		if mag > 0 {
			powerDB[i] = 10 * log10(mag)
		} else {
			powerDB[i] = -300 // floor
		}
	}

	return &FFTResult{
		Frequencies: freqs,
		Magnitudes:  mags,
		PowerDB:     powerDB,
		SampleRate:  sampleRate,
	}, nil
}

// ComputeIQFFT performs a complex FFT on interleaved I/Q samples.
func ComputeIQFFT(iq []float64, sampleRate uint32) (*FFTResult, error) {
	n := len(iq) / 2 // number of IQ pairs
	if n == 0 {
		return nil, nil
	}

	nfft := 1
	for nfft < n {
		nfft <<= 1
	}

	re := make([]float64, nfft)
	im := make([]float64, nfft)
	for i := 0; i < n; i++ {
		re[i] = iq[i*2]     // I
		im[i] = iq[i*2+1]   // Q
	}

	fourier.FFT(true, re, im)

	half := nfft / 2
	freqs := make([]float64, half)
	mags := make([]float64, half)
	powerDB := make([]float64, half)
	df := float64(sampleRate) / float64(nfft)

	for i := 0; i < half; i++ {
		freqs[i] = float64(i) * df
		mag := re[i]*re[i] + im[i]*im[i]
		mags[i] = mag
		if mag > 0 {
			powerDB[i] = 10 * log10(mag)
		} else {
			powerDB[i] = -300
		}
	}

	return &FFTResult{
		Frequencies: freqs,
		Magnitudes:  mags,
		PowerDB:     powerDB,
		SampleRate:  sampleRate,
	}, nil
}

// log10 is a helper to avoid importing math just for this.
func log10(x float64) float64 {
	// Use the math package properly
	return log10impl(x)
}