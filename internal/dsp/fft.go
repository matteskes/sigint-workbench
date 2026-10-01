// Package dsp provides digital signal processing primitives.
package dsp

import (
	"math"

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

	// Build complex slice
	// gonum real FFT: dst has length nfft/2+1, src has length nfft
	half := nfft / 2
	cx := make([]complex128, half+1)

	// Perform forward FFT in-place
	fft := fourier.NewFFT(nfft)
	re := make([]float64, nfft)
	copy(re, samples)
	fft.Coefficients(cx, re)

	// Compute one-sided spectrum
	freqs := make([]float64, half)
	mags := make([]float64, half)
	powerDB := make([]float64, half)
	df := float64(sampleRate) / float64(nfft)

	for i := 0; i < half; i++ {
		freqs[i] = float64(i) * df
		c := cx[i]
		mag := real(c)*real(c) + imag(c)*imag(c)
		mags[i] = mag
		if mag > 0 {
			powerDB[i] = 10 * math.Log10(mag)
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

	cx := make([]complex128, nfft)
	for i := 0; i < n; i++ {
		cx[i] = complex(iq[i*2], iq[i*2+1]) // I, Q
	}

	fft := fourier.NewCmplxFFT(nfft)
	fft.Coefficients(cx, cx)

	half := nfft / 2
	freqs := make([]float64, half)
	mags := make([]float64, half)
	powerDB := make([]float64, half)
	df := float64(sampleRate) / float64(nfft)

	for i := 0; i < half; i++ {
		freqs[i] = float64(i) * df
		c := cx[i]
		mag := real(c)*real(c) + imag(c)*imag(c)
		mags[i] = mag
		if mag > 0 {
			powerDB[i] = 10 * math.Log10(mag)
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