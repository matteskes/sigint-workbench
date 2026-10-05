package tfr

import (
	"math"
	"os"

	"gonum.org/v1/gonum/dsp/fourier"

	"sigint-workbench/internal/db"
)

// reassignCompute renders the §19.2 reassigned spectrogram: the STFT
// followed by reassignment (Kodera/Flandrin) — each (t, f) cell moves
// to its phase-gradient energy centroid before pooling. The efficient
// three-window form is used per frame (Nelson 2001): X (window h),
// Xt (window t·h for the group delay t̂), Xd (window dh/dt for the
// local frequency f̂):
//
//	t̂ = frame_center + Re{Xt/X}        (samples)
//	f̂ = ω_bin − Im{Xd/X}               (rad/sample)
//
// The frame's energy |X|² lands at (t̂, f̂), rounding into the bounded
// grid. Noise-dominated cells scatter — the §19.2 artifact the
// response metadata names.
func reassignCompute(f *os.File, rec *db.Recording, req Request, startSample, spanSamples int64, res *Result) error {
	plan := planFrames(req, spanSamples)
	fs := float64(rec.SampleRate)
	nfft := req.NFFT

	binLo, binCnt := bandBins(nfft, fs, res.FreqLoHz-float64(rec.CenterFreq), res.FreqHiHz-float64(rec.CenterFreq))
	rows := binCnt
	if rows > MaxRows {
		rows = MaxRows
	}
	g := newGrid(rows, plan.cols)

	w, dw := windowAt(req.WindowVal(), nfft)
	nC := float64(nfft / 2)
	// Time-weighted window: (i − center)·h so the group-delay ratio is
	// relative to the window center (t̂ = center + Re{Xt/X}).
	wt := make([]float64, nfft)
	for i := 0; i < nfft; i++ {
		wt[i] = (float64(i) - nC) * w[i]
	}

	fft := fourier.NewCmplxFFT(nfft)
	x := make([]complex128, nfft)
	dst := make([]complex128, nfft)
	dstT := make([]complex128, nfft)
	dstD := make([]complex128, nfft)

	// slider keeps the current window; advance by hop per frame.
	sl, err := newSlider(f, startSample, startSample+spanSamples, nfft)
	if err != nil {
		return err
	}

	dfBin := 2 * math.Pi / float64(nfft) // rad/sample per bin
	for k := 0; k < plan.frames; k++ {
		for i := 0; i < nfft; i++ {
			x[i] = sl.ring[i] * complex(w[i], 0)
		}
		fft.Coefficients(dst, x)
		for i := 0; i < nfft; i++ {
			x[i] = sl.ring[i] * complex(wt[i], 0)
		}
		fft.Coefficients(dstT, x)
		for i := 0; i < nfft; i++ {
			x[i] = sl.ring[i] * complex(dw[i], 0)
		}
		fft.Coefficients(dstD, x)

		for b := 0; b < nfft; b++ {
			p := powerAt(dst[b])
			if p < 1e-30 {
				continue // silent bin: nothing to reassign
			}
			// Re{Xt/X} = (Xt·conj(X))_real / |X|²
			rr := (real(dstT[b])*real(dst[b]) + imag(dstT[b])*imag(dst[b])) / p
			// Im{Xd/X} = (Xd·conj(X))_imag / |X|²
			imD := (real(dstD[b])*imag(dst[b]) - imag(dstD[b])*real(dst[b])) / p
			tHat := nC + rr                // group delay within the window
			fHat := float64(b)*dfBin - imD // local frequency, rad/sample
			// Absolute sample time of the centroid, then into the grid.
			tAbs := float64(sl.head) + tHat
			colF := (tAbs - float64(startSample)) / float64(spanSamples) * float64(plan.cols)
			// f̂ (rad/sample) → shifted (ascending) bin space → row.
			binShifted := fHat/(2*math.Pi)*float64(nfft) + float64(nfft)/2
			rowF := (binShifted - float64(binLo)) / float64(binCnt) * float64(rows)
			g.poolCell(rowF, colF, p)
		}
		if err := sl.advance(plan.hop); err != nil {
			return err
		}
	}
	res.Tile, res.DBRef = g.finish()
	res.Rows, res.Cols = g.rows, g.cols
	res.Hop, res.Overlap, res.Note = plan.hop, req.OverlapVal(), plan.note
	return nil
}

func powerAt(c complex128) float64 {
	return real(c)*real(c) + imag(c)*imag(c)
}
