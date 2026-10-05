package tfr

import (
	"math"
	"os"

	"gonum.org/v1/gonum/dsp/fourier"

	"sigint-workbench/internal/db"
)

// windowAt returns the periodic (DFT-even) analysis window of the
// given name and length, plus its sample-by-sample derivative dh/dn
// (used by reassignment's frequency estimate). gaussian is the Gabor
// transform (uncertainty-bound optimal, §19.2 reference); hamming is
// the shipped default; rectangular has a zero derivative in its
// interior (reassignment rejects it at validation).
func windowAt(name string, n int) (w, dw []float64) {
	w = make([]float64, n)
	dw = make([]float64, n)
	switch name {
	case WindowGaussian:
		// σ = n/8 → ≈±4σ of support at the window edges.
		sigma := float64(n) / 8
		norm := 1 / (2 * sigma * sigma)
		for i := 0; i < n; i++ {
			d := float64(i - n/2)
			w[i] = math.Exp(-d * d * norm)
			dw[i] = -2 * d * norm * w[i]
		}
	case WindowRectangular:
		for i := range w {
			w[i] = 1
		}
	default: // WindowHamming
		for i := 0; i < n; i++ {
			t := 2 * math.Pi * float64(i) / float64(n)
			w[i] = 0.54 - 0.46*math.Cos(t)
			dw[i] = 0.46 * (2 * math.Pi / float64(n)) * math.Sin(t)
		}
	}
	return w, dw
}

// bandBins maps the cropped offset band [loOff, hiOff] (Hz relative
// to the recording center) onto shifted (ascending) bin indices of an
// nfft-point FFT at sample rate fs.
func bandBins(nfft int, fs, loOff, hiOff float64) (binLo, binCnt int) {
	df := fs / float64(nfft)
	b0 := int(math.Floor((loOff + fs/2) / df))
	b1 := int(math.Ceil((hiOff + fs/2) / df))
	if b0 < 0 {
		b0 = 0
	}
	if b1 > nfft {
		b1 = nfft
	}
	if b1 <= b0 {
		return 0, nfft
	}
	return b0, b1 - b0
}

// stftCompute renders the §19.2 stft spectrogram: windowed FFT per
// hop, power max-pooled online into the bounded grid.
func stftCompute(f *os.File, rec *db.Recording, req Request, startSample, spanSamples int64, res *Result) error {
	plan := planFrames(req, spanSamples)
	fs := float64(rec.SampleRate)
	nfft := req.NFFT

	binLo, binCnt := bandBins(nfft, fs, res.FreqLoHz-float64(rec.CenterFreq), res.FreqHiHz-float64(rec.CenterFreq))
	rows := binCnt
	if rows > MaxRows {
		rows = MaxRows
	}
	g := newGrid(rows, plan.cols)

	w, _ := windowAt(req.WindowVal(), nfft)
	fft := fourier.NewCmplxFFT(nfft)
	src := make([]complex128, nfft)
	dst := make([]complex128, nfft)

	sl, err := newSlider(f, startSample, startSample+spanSamples, nfft)
	if err != nil {
		return err
	}
	for k := 0; k < plan.frames; k++ {
		for i := 0; i < nfft; i++ {
			src[i] = sl.ring[i] * complex(w[i], 0)
		}
		fft.Coefficients(dst, src)
		colF := float64(k) * float64(plan.cols) / float64(plan.frames)
		shifted := fftshiftPows(powerBins(dst))
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

// powerBins converts an FFT output vector to linear power.
func powerBins(bins []complex128) []float64 {
	p := make([]float64, len(bins))
	for i, c := range bins {
		p[i] = real(c)*real(c) + imag(c)*imag(c)
	}
	return p
}
