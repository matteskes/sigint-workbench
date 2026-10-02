// Package dsp — Butterworth IIR filters (bilinear-transformed analog
// prototype, cascaded second-order sections).
package dsp

import "math"

// FilterKind selects a biquad topology.
type FilterKind int

const (
	// Lowpass passes frequencies below f0.
	Lowpass FilterKind = iota
	// Highpass passes frequencies above f0.
	Highpass
	// Bandpass passes frequencies around f0 (bandwidth set by Q).
	Bandpass
)

// Biquad is a second-order IIR section in transposed direct form II.
// Not safe for concurrent use.
type Biquad struct {
	b0, b1, b2 float64 // feedforward (a0 normalized to 1)
	a1, a2     float64 // feedback
	x1, x2     float64 // input state
	y1, y2     float64 // output state
}

// Process filters one sample.
func (s *Biquad) Process(x float64) float64 {
	y := s.b0*x + s.b1*s.x1 + s.b2*s.x2 - s.a1*s.y1 - s.a2*s.y2
	s.x2, s.x1 = s.x1, x
	s.y2, s.y1 = s.y1, y
	return y
}

// identity returns an all-pass (gain 1) section.
func identity() Biquad { return Biquad{b0: 1} }

// designSecondOrder designs a second-order section from the analog
// prototype via the bilinear transform. For Butterworth low-/high-pass
// sections, q is the section quality factor; for bandpass it is the
// requested Q.
func designSecondOrder(kind FilterKind, f0, q, fs float64) Biquad {
	if f0 <= 0 || fs <= 0 || q <= 0 {
		return identity()
	}
	if f0 >= fs/2 {
		f0 = fs / 2 * 0.999
	}
	w0 := 2 * math.Pi * f0 / fs
	sinW, cosW := math.Sin(w0), math.Cos(w0)
	alpha := sinW / (2 * q)

	var b0, b1, b2, a0, a1, a2 float64
	switch kind {
	case Lowpass:
		b0, b1, b2 = (1-cosW)/2, 1 - cosW, (1-cosW)/2
		a0, a1, a2 = 1+alpha, -2*cosW, 1 - alpha
	case Highpass:
		b0, b1, b2 = (1+cosW)/2, -(1 + cosW), (1+cosW)/2
		a0, a1, a2 = 1+alpha, -2*cosW, 1 - alpha
	case Bandpass:
		b0, b1, b2 = alpha, 0, -alpha
		a0, a1, a2 = 1+alpha, -2*cosW, 1 - alpha
	}
	return Biquad{b0: b0 / a0, b1: b1 / a0, b2: b2 / a0, a1: a1 / a0, a2: a2 / a0}
}

// designFirstOrder designs the single-pole section used when the
// filter order is odd (prewarped first-order prototype).
func designFirstOrder(kind FilterKind, f0, fs float64) Biquad {
	if f0 <= 0 || fs <= 0 {
		return identity()
	}
	if f0 >= fs/2 {
		f0 = fs / 2 * 0.999
	}
	// Prewarped analog cutoff.
	omega := 2 * fs * math.Tan(math.Pi*f0/fs)
	// Bilinear transform with T = 1/fs: s = 2fs(z-1)/(z+1).
	// 1st order: H(z) = b0(1+z^-1) / (1 + a1 z^-1)
	var b0, b1 float64
	switch kind {
	case Lowpass: // H(s) = 1 / (1 + s/Ω)
		b0, b1 = omega, omega
	case Highpass: // H(s) = s / (1 + s/Ω)
		b0, b1 = 2*fs, -2*fs
	default:
		return identity()
	}
	denom := omega + 2*fs
	return Biquad{b0: b0 / denom, b1: b1 / denom, a1: (omega - 2*fs) / denom}
}

// Butterworth designs an N-th order Butterworth filter as a cascade of
// biquad sections (first-order section for odd orders). For Lowpass and
// Highpass the section Q values are those of the N-th order analog
// prototype; q is ignored. For Bandpass, q is the quality factor of
// each of the `order` cascaded sections (q = f0/bandwidth for order 2).
func Butterworth(kind FilterKind, f0, q, fs float64, order int) []Biquad {
	if order < 1 {
		order = 1
	}
	if kind == Bandpass {
		if q <= 0 {
			q = 1 / math.Sqrt2
		}
		sections := make([]Biquad, 0, order)
		for i := 0; i < order; i++ {
			sections = append(sections, designSecondOrder(Bandpass, f0, q, fs))
		}
		return sections
	}

	// Analog prototype poles of the N-th order Butterworth:
	// s_k = exp(i·π·(2k+N-1)/(2N)), paired as conjugates.
	sections := make([]Biquad, 0, (order+1)/2)
	for k := 0; 2*k+1 <= order; k++ {
		if order%2 == 1 && k == order/2 {
			// Single real pole (odd order).
			sections = append(sections, designFirstOrder(kind, f0, fs))
			continue
		}
		theta := math.Pi * (2*float64(k) + float64(order) - 1) / (2 * float64(order))
		qk := 1 / (2 * math.Abs(math.Cos(theta)))
		sections = append(sections, designSecondOrder(kind, f0, qk, fs))
	}
	return sections
}

// FilterBiquads applies a cascade of sections to a sample slice.
// An empty cascade returns the input unchanged.
func FilterBiquads(samples []float64, sections []Biquad) []float64 {
	if len(samples) == 0 || len(sections) == 0 {
		return samples
	}
	out := make([]float64, 0, len(samples))
	for _, s := range samples {
		y := float64(s)
		for _, sec := range sections {
			y = sec.Process(y)
		}
		out = append(out, y)
	}
	return out
}