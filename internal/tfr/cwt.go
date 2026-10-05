package tfr

import (
	"fmt"
	"math"
	"os"

	"sigint-workbench/internal/db"
)

// cwtCompute renders the §19.2 continuous Morlet wavelet transform
// (ω0 = 6): log-spaced scales mapped to pseudo-frequencies spanning
// [fs/nfft, fs/4] (the Morlet band of trust), cropped by freqSpan.
// Columns sample the span at a bounded stride; each kernel is
// energy-normalized so row magnitudes stay comparable across scales.
// Scale rows smear over proportionally longer time at low frequency —
// the §19.2 dominant artifact the response names.
func cwtCompute(f *os.File, rec *db.Recording, req Request, startSample, spanSamples int64, res *Result) error {
	fs := float64(rec.SampleRate)
	const omega0 = 6.0

	// Pseudo-frequency band (Morlet: f = ω0/(2πa)).
	fLo := fs / float64(req.NFFT)
	fHi := fs / 4
	note := ""
	if lo, hi := res.FreqLoHz-float64(rec.CenterFreq), res.FreqHiHz-float64(rec.CenterFreq); hi > lo {
		if lo > fLo {
			fLo = lo
		}
		if hi < fHi {
			fHi = hi
		}
	}
	if fHi <= fLo*1.0001 {
		fLo = fs / float64(req.NFFT)
		fHi = fs / 4
		note = fmt.Sprintf("freqSpan outside the Morlet band; rendered full band")
	}

	scales := req.NFFT
	if scales > MaxRows {
		scales = MaxRows
		if note != "" {
			note += "; "
		}
		note += fmt.Sprintf("scale count capped at %d", MaxRows)
	}
	if scales < 2 {
		scales = 2
	}

	cols := MaxCols
	if int(spanSamples) < cols {
		cols = int(spanSamples)
	}
	if cols < 1 {
		cols = 1
	}
	stride := int(spanSamples) / cols
	if stride < 1 {
		stride = 1
	}

	// Per-scale Morlet kernels, energy-normalized. Log-spaced
	// pseudo-frequencies; support ±3σ.
	type scaleKernel struct {
		kern []complex128 // conj(ψ), index 0 = t = −3a
		rad  int          // support radius in samples (3a)
		freq float64      // pseudo-frequency
	}
	fl := math.Log(fLo)
	fh := math.Log(fHi)
	kernels := make([]scaleKernel, scales)
	aMax := 0
	ln2pi := math.Log(math.Sqrt(math.Pi * 2)) // not used; π^{−1/4} amplitude folded into normalization
	_ = ln2pi
	for s := 0; s < scales; s++ {
		var fq float64
		if scales == 1 {
			fq = fLo
		} else {
			fq = math.Exp(fl + (fh-fl)*float64(s)/float64(scales-1))
		}
		a := omega0 * fs / (2 * math.Pi * fq)
		rad := int(math.Ceil(3 * a))
		if rad < 1 {
			rad = 1
		}
		kern := make([]complex128, 2*rad+1)
		sum := 0.0
		amp := math.Pow(a, 0.25)
		for i := -rad; i <= rad; i++ {
			t := float64(i) / a
			env := math.Exp(-t * t / 2)
			v := complex(env*math.Cos(omega0*t)/amp, env*math.Sin(omega0*t)/amp)
			kern[i+rad] = cmplxConj(v)
			sum += real(v)*real(v) + imag(v)*imag(v)
		}
		norm := 1 / math.Sqrt(sum) // energy normalization (Σ|ψ|² = 1)
		for i := range kern {
			kern[i] = complex(real(kern[i])*norm, imag(kern[i])*norm)
		}
		kernels[s] = scaleKernel{kern: kern, rad: rad, freq: fq}
		if rad > aMax {
			aMax = rad
		}
	}

	g := newGrid(scales, cols)
	need := 2*aMax + 1
	sl, err := newSlider(f, startSample-int64(aMax), startSample+spanSamples, need)
	if err != nil {
		return err
	}
	buf := make([]complex128, 0, 2*aMax+1)
	for c := 0; c < cols; c++ {
		for s := 0; s < scales; s++ {
			k := kernels[s]
			var acc complex128
			buf = buf[:0]
			for i := -k.rad; i <= k.rad; i++ {
				buf = append(buf, sl.ring[aMax+i])
			}
			for i := range buf {
				acc += buf[i] * k.kern[i]
			}
			p := real(acc)*real(acc) + imag(acc)*imag(acc)
			// Column center maps to the grid column; row = scale.
			colF := float64(c)
			g.poolCell(float64(scales-1-s), colF, p) // row 0 = fLo (lowest)
		}
		if err := sl.advance(stride); err != nil {
			return err
		}
	}
	res.Tile, res.DBRef = g.finish()
	res.Rows, res.Cols = g.rows, g.cols
	res.Hop, res.Overlap, res.Note = stride, req.OverlapVal(), note
	res.FreqLoHz = float64(rec.CenterFreq) + fLo
	res.FreqHiHz = float64(rec.CenterFreq) + fHi
	return nil
}

// cmplxConj avoids importing math/cmplx for one call.
func cmplxConj(c complex128) complex128 {
	return complex(real(c), -imag(c))
}
