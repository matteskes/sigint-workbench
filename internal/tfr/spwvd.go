package tfr

import (
	"math"
	"math/cmplx"
	"os"

	"gonum.org/v1/gonum/dsp/fourier"

	"sigint-workbench/internal/db"
)

// spwvdCompute renders the §19.2 smoothed pseudo Wigner-Ville
// distribution with separable time/frequency kernels:
//
//	K(m, l) = Σ_p h(p)·x(m+u+l)·x*(m+u−l)     (time-smoothed lag product)
//	W(m, k) = Σ_l K(m, l)·g(l)·e^{−j2πkl/L2}   (lag FFT, L2 = 2·nfft)
//
// Bin geometry note: with integer lags the lag-domain frequency is
// ν = 2f/fs, so the unaliased band is ±fs/4 at df = fs/(4nfft) — half
// the STFT's span at half its spacing (content beyond ±fs/4 folds,
// part of the §19.2 artifact). Kernel sizes are bounded (≤ 1024 lags,
// ≤ 129 taps) so per-frame cost stays flat at large nfft.
func spwvdCompute(f *os.File, rec *db.Recording, req Request, startSample, spanSamples int64, res *Result) error {
	plan := planFrames(req, spanSamples)
	fs := float64(rec.SampleRate)
	nfft := req.NFFT

	// Usable WVD band: ±fs/4 at df = fs/(4nfft). bandBins with an
	// effective rate of fs/2 yields exactly that bin set over L2.
	loOff := math.Max(res.FreqLoHz-float64(rec.CenterFreq), -fs/4)
	hiOff := math.Min(res.FreqHiHz-float64(rec.CenterFreq), fs/4)
	L2 := 2 * nfft
	binLo, binCnt := bandBins(L2, fs/2, loOff, hiOff)
	rows := binCnt
	if rows > MaxRows {
		rows = MaxRows
	}
	g := newGrid(rows, plan.cols)
	res.FreqLoHz = float64(rec.CenterFreq) + loOff
	res.FreqHiHz = float64(rec.CenterFreq) + hiOff

	// Bounded separable kernels (odd lengths, centered).
	p := nfft/16 + 1
	if p > 129 {
		p = 129
	}
	if p%2 == 0 {
		p++
	}
	lmax := nfft / 4
	if lmax > 1024 {
		lmax = 1024
	}
	h := hammingTaps(p)           // time smoothing
	gl := hammingTaps(2*lmax + 1) // lag smoothing, index l+lmax

	need := p + 2*lmax // ring extent around the frame center
	sl, err := newSlider(f, startSample-int64(p/2+lmax), startSample+spanSamples, need)
	if err != nil {
		return err
	}
	ci := p/2 + lmax // ring index of the frame center sample

	lags := make([]complex128, L2)
	out := make([]complex128, L2)
	fft := fourier.NewCmplxFFT(L2)
	for k := 0; k < plan.frames; k++ {
		for i := range lags {
			lags[i] = 0
		}
		for l := -lmax; l <= lmax; l++ {
			var acc complex128
			for t := 0; t < p; t++ {
				u := t - p/2
				acc += sl.ring[ci+u+l] * cmplx.Conj(sl.ring[ci+u-l]) * complex(h[t], 0)
			}
			lags[(l+L2)%L2] = acc * complex(gl[l+lmax], 0)
		}
		fft.Coefficients(out, lags)
		colF := float64(k) * float64(plan.cols) / float64(plan.frames)
		shifted := fftshiftPows(powerBins(out))
		g.poolFrame(shifted[binLo:binLo+binCnt], binCnt, colF)
		if err := sl.advance(plan.hop); err != nil {
			return err
		}
	}
	res.Tile, res.DBRef = g.finish()
	res.Rows, res.Cols = g.rows, g.cols
	res.Hop, res.Overlap, res.Note = plan.hop, req.OverlapVal(), plan.note
	return nil
}

// hammingTaps returns a symmetric Hamming window of n taps.
func hammingTaps(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}
