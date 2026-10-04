package tdoa

import (
	"math"
	"math/cmplx"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Delay estimates one pair's delay via GCC-PHAT (§9.5 step 2): the
// cross-spectrum is normalized to unit magnitude, inverse
// transformed, and the peak refined parabolically to sub-sample
// delay. The returned TauNS is τᵢⱼ = τᵢ − τⱼ = (dᵢ − dⱼ)/c for
// a = window(i), b = window(j) — positive when the emitter is
// farther from a's receiver. maxTauNS bounds the lag search (use the
// c-scaled maximum baseline plus margin); Peak is the normalized
// peak prominence in [0, 1].
func Delay(a, b Window, maxTauNS float64) (PairDelay, bool) {
	n := len(a.Samples)
	if n == 0 || len(b.Samples) != n || a.SampleRate <= 0 {
		return PairDelay{}, false
	}
	nfft := nextPow2(2 * n)
	fa := make([]complex128, nfft)
	fb := make([]complex128, nfft)
	copy(fa, a.Samples)
	copy(fb, b.Samples)
	fwd := fourier.NewCmplxFFT(nfft)
	fwd.Coefficients(fa, fa)
	fwd.Coefficients(fb, fb)
	for k := range fa {
		fa[k] = fa[k] * cmplx.Conj(fb[k])
	}
	// PHAT with a noise gate: normalize only bins with meaningful
	// energy. Whitening the FFT round-off / noise floor floods the
	// correlation with tens of thousands of unit-magnitude junk
	// bins and destroys the peak — the classic PHAT failure mode.
	maxMag := 0.0
	for k := range fa {
		if m := cmplx.Abs(fa[k]); m > maxMag {
			maxMag = m
		}
	}
	for k := range fa {
		m := cmplx.Abs(fa[k])
		if m > 1e-3*maxMag {
			fa[k] /= complex(m, 0)
		} else {
			fa[k] = 0
		}
	}
	r := ifft(fa)

	maxLag := int(math.Ceil(maxTauNS * 1e-9 * a.SampleRate))
	if maxLag <= 0 || maxLag > nfft/4 {
		maxLag = nfft / 4
	}
	sum := 0.0
	for i := range r {
		sum += cmplx.Abs(r[i])
	}
	mean := sum / float64(nfft)
	best, bestM := -1.0, 0
	for m := -maxLag; m <= maxLag; m++ {
		idx := m
		if idx < 0 {
			idx += nfft
		}
		if v := cmplx.Abs(r[idx]); v > best {
			best, bestM = v, m
		}
	}
	if best <= 0 || mean <= 0 {
		return PairDelay{}, false
	}
	// Parabolic vertex through the peak's two neighbors.
	at := func(m int) float64 {
		if m < 0 {
			m += nfft
		}
		return cmplx.Abs(r[m%nfft])
	}
	y0, y2 := at(bestM-1), at(bestM+1)
	den := y0 + y2 - 2*best
	delta := 0.0
	if den < -1e-15 {
		delta = 0.5 * (y0 - y2) / den
		if delta > 1 {
			delta = 1
		} else if delta < -1 {
			delta = -1
		}
	}
	return PairDelay{
		TauNS: (float64(bestM) + delta) / a.SampleRate * 1e9,
		Peak:  math.Max(0, math.Min(1, 1-mean/best)),
	}, true
}

// ifft inverts via conjugation: ifft(z) = conj(fft(conj(z)))/n.
func ifft(z []complex128) []complex128 {
	fwd := fourier.NewCmplxFFT(len(z))
	for i := range z {
		z[i] = cmplx.Conj(z[i])
	}
	fwd.Coefficients(z, z)
	inv := 1 / complex(float64(len(z)), 0)
	for i := range z {
		z[i] = cmplx.Conj(z[i]) * inv
	}
	return z
}

// nextPow2 returns the smallest power of two ≥ n.
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
