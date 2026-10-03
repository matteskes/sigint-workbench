// Package dsp provides digital signal processing primitives.
package dsp

import (
	"math"

	"gonum.org/v1/gonum/dsp/fourier"
)

// FFTResult holds the output of an FFT computation.
//
// ComputeFFT returns a one-sided (positive-frequency) spectrum of N/2 bins;
// ComputeIQFFT returns the full wrapped two-sided spectrum of N bins with
// signed offsets (D4, §5.3) so a signal below the tuning center is
// detectable and its absolute frequency (center + offset) is correct.
type FFTResult struct {
	Frequencies []float64 // Hz offset per bin (see above)
	Magnitudes  []float64 // linear magnitude, len == len(Frequencies)
	PowerDB     []float64 // power in dB, len == len(Frequencies)
	SampleRate  uint32
	BinSpacing  float64 // Hz per bin (sampleRate / nfft)
}

// PositiveHalf returns a copy of the positive one-sided portion of a full
// wrapped IQ spectrum (bins [0, fs/2)) in the exact form models/train.py was
// trained on. The full wrapped FFTResult is used for peak detection and
// offset reporting (D4), but the ONNX feature vector must stay
// byte-compatible with training, so feature extraction uses this view.
func (r *FFTResult) PositiveHalf() *FFTResult {
	if r == nil {
		return nil
	}
	half := len(r.Frequencies) / 2
	freqs := make([]float64, half)
	mags := make([]float64, half)
	powerDB := make([]float64, half)
	copy(freqs, r.Frequencies[:half])
	copy(mags, r.Magnitudes[:half])
	copy(powerDB, r.PowerDB[:half])
	return &FFTResult{
		Frequencies: freqs,
		Magnitudes:  mags,
		PowerDB:     powerDB,
		SampleRate:  r.SampleRate,
		BinSpacing:  r.BinSpacing,
	}
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
		BinSpacing:  df,
	}, nil
}

// ComputeIQFFT performs a complex FFT on interleaved I/Q samples.
//
// It returns the full wrapped two-sided spectrum (D4, §5.3): bins
// 0..nfft/2-1 are the positive offsets [0, fs/2) and bins nfft/2..nfft-1 are
// the negative offsets [-fs/2, 0). Frequencies holds the signed baseband
// offset so the caller can report the absolute frequency as center + offset.
// Use PositiveHalf for a train-compatible one-sided view.
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

	freqs := make([]float64, nfft)
	mags := make([]float64, nfft)
	powerDB := make([]float64, nfft)
	df := float64(sampleRate) / float64(nfft)

	for i := 0; i < nfft; i++ {
		if i < nfft/2 {
			freqs[i] = float64(i) * df
		} else {
			freqs[i] = float64(i-nfft) * df
		}
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
		BinSpacing:  df,
	}, nil
}

// log10 is a helper to avoid importing math just for this.
func log10(x float64) float64 {
	// Use the math package properly
	return log10impl(x)
}