package tfr

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"sigint-workbench/internal/db"
)

// Result is the §19.3 response body: numeric tiles plus metadata
// (method, span, bin geometry, dominant artifact — §19.3/§19.2).
type Result struct {
	RecordingID string  `json:"recordingId"`
	Method      string  `json:"method"`
	Window      string  `json:"window,omitempty"` // stft/reassigned only
	T0          float64 `json:"t0"`
	T1          float64 `json:"t1"`
	NFFT        int     `json:"nfft"`
	Hop         int     `json:"hop"`     // samples between analysis frames (stride for cwt)
	Overlap     float64 `json:"overlap"` // effective
	SampleRate  uint32  `json:"sampleRate"`
	CenterFreq  uint64  `json:"centerFreq"` // absolute Hz (recording center)
	FreqLoHz    float64 `json:"freqLoHz"`   // rendered band edges, absolute Hz
	FreqHiHz    float64 `json:"freqHiHz"`
	Rows        int     `json:"rows"`   // frequency rows, row 0 = FreqLoHz
	Cols        int     `json:"cols"`   // time columns, col 0 = T0
	DBRef       float64 `json:"dbRef"`  // dB value at tile value 0 (the max)
	DBStep      float64 `json:"dbStep"` // dB per LSB (1.0; floored at −128)
	Tile        []int8  `json:"tile"`   // rows×cols, row-major
	Artifact    string  `json:"artifact"`
	ElapsedMS   int64   `json:"elapsedMs"`
	Note        string  `json:"note,omitempty"` // e.g. hop widened to bound frames
}

// grid is the bounded output matrix: power max-pooled online into
// rows×cols cells (row 0 = lowest rendered frequency, col 0 = T0).
type grid struct {
	rows, cols int
	pow        []float64
}

func newGrid(rows, cols int) *grid {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	return &grid{rows: rows, cols: cols, pow: make([]float64, rows*cols)}
}

// poolCell max-accumulates one continuous (row, col) coordinate.
func (g *grid) poolCell(rowF, colF, power float64) {
	r := int(math.Floor(rowF))
	c := int(math.Floor(colF))
	if r < 0 || r >= g.rows || c < 0 || c >= g.cols || power <= 0 {
		return
	}
	if i := r*g.cols + c; power > g.pow[i] {
		g.pow[i] = power
	}
}

// finish converts pooled power to int8 dB relative to the tile max
// (0 = max, 1 dB/LSB, floor −128; untouched cells land on the floor).
func (g *grid) finish() (tile []int8, dbRef float64) {
	maxP := 0.0
	for _, p := range g.pow {
		if p > maxP {
			maxP = p
		}
	}
	dbRef = 10 * math.Log10(maxP+1e-300)
	tile = make([]int8, len(g.pow))
	for i, p := range g.pow {
		db := 10*math.Log10(p+1e-300) - dbRef
		v := int(math.Round(db))
		if v < -128 {
			v = -128
		}
		if v > 0 {
			v = 0
		}
		tile[i] = int8(v)
	}
	return tile, dbRef
}

// iqSamples reads count complex samples starting at sample index start
// (int16 LE interleaved I/Q pairs, §10.5) into dst. Indices before 0
// and past EOF pad zeros so edge windows render instead of erroring —
// a frame whose extent precedes the recording start (SPWVD/CWT
// kernels) still computes.
func iqSamples(f *os.File, scratch []byte, start int64, dst []complex128) error {
	// Zero the virtual prefix (negative sample indices).
	i0 := int64(0)
	if start < 0 {
		i0 = -start
		if i0 > int64(len(dst)) {
			i0 = int64(len(dst))
		}
	}
	for i := int64(0); i < i0; i++ {
		dst[i] = 0
	}
	rest := dst[i0:]
	if len(rest) == 0 {
		return nil
	}
	need := int64(len(rest)) * 4
	if int64(len(scratch)) < need {
		return fmt.Errorf("tfr: scratch too small")
	}
	s := scratch[:need]
	n, err := f.ReadAt(s, (start+i0)*4)
	if err != nil && err != io.EOF {
		return fmt.Errorf("tfr: read iq: %w", err)
	}
	for i := range rest {
		o := i * 4
		var re, im int16
		if o+1 < n {
			re = int16(binary.LittleEndian.Uint16(s[o:]))
		}
		if o+3 < n {
			im = int16(binary.LittleEndian.Uint16(s[o+2:]))
		}
		rest[i] = complex(float64(re)/32768, float64(im)/32768)
	}
	return nil
}

// slider streams a window over the span with bounded memory: the ring
// always covers samples [head, head+need) and advances monotonically.
type slider struct {
	f       *os.File
	scratch []byte
	ring    []complex128
	head    int64 // sample index of ring[0]
	end     int64 // exclusive span end (sample index)
}

func newSlider(f *os.File, start, end int64, need int) (*slider, error) {
	if need < 1 {
		return nil, fmt.Errorf("tfr: slider need < 1")
	}
	s := &slider{
		f:       f,
		scratch: make([]byte, need*4),
		ring:    make([]complex128, need),
		head:    start,
		end:     end,
	}
	if err := iqSamples(f, s.scratch, start, s.ring); err != nil {
		return nil, err
	}
	return s, nil
}

// advance moves the ring head forward by n samples (n ≥ 0), keeping
// the tail and refilling from the file; samples past end read as
// zeros so trailing frames render.
func (s *slider) advance(n int) error {
	if n <= 0 {
		return nil
	}
	if n >= len(s.ring) {
		s.head += int64(n)
		return iqSamples(s.f, s.scratch, s.head, s.ring)
	}
	copy(s.ring, s.ring[n:])
	s.head += int64(n)
	return iqSamples(s.f, s.scratch, s.head+int64(len(s.ring))-int64(n), s.ring[len(s.ring)-n:])
}

// poolFrame max-accumulates a full analysis frame of power bins in
// shifted order (index 0 = lowest rendered frequency) into column
// colF, squeezing rowBins bins into the grid's rows.
func (g *grid) poolFrame(shifted []float64, rowBins int, colF float64) {
	for b := 0; b < rowBins && b < len(shifted); b++ {
		rowF := float64(b) * float64(g.rows) / float64(rowBins)
		g.poolCell(rowF, colF, shifted[b])
	}
}

// fftshiftPows permutes unshifted FFT power bins into ascending
// frequency order (index 0 = −fs/2), matching the §18 feed convention.
func fftshiftPows(pows []float64) []float64 {
	n := len(pows)
	out := make([]float64, n)
	half := n / 2
	copy(out, pows[half:])          // negative offsets first
	copy(out[n-half:], pows[:half]) // then positive
	return out
}

// framePlan is the shared geometry for the FFT-frame methods
// (stft/reassigned/spwvd): span in samples, frame stride, cropped
// frequency rows, and the bounded output grid.
type framePlan struct {
	startSample int64
	endSample   int64 // exclusive
	spanSamples int64
	hop         int
	frames      int
	rows        int // grid rows (≤ MaxRows)
	cols        int // grid cols (≤ MaxCols)
	note        string
}

// planFrames derives the streaming geometry. The requested hop is
// nfft·(1−overlap); when that would exceed MaxFrames over the span,
// the hop widens to fit and the note says so (§19.2 bounds the
// request, the engine bounds the work — both are reported).
func planFrames(req Request, spanSamples int64) framePlan {
	span := spanSamples
	hop := int(math.Round(float64(req.NFFT) * (1 - req.OverlapVal())))
	if hop < 1 {
		hop = 1
	}
	note := ""
	maxFrames := frameCap
	if req.NFFT > 0 {
		// Bound total window work: frames×nfft ≤ ~2e8 element shifts.
		if w := int(2e8 / float64(req.NFFT)); w < maxFrames {
			maxFrames = w
		}
	}
	frames := 1
	if span > int64(req.NFFT) {
		frames = int((span-int64(req.NFFT))/int64(hop)) + 1
		if frames > maxFrames {
			frames = maxFrames
			hop = int(math.Ceil(float64(span-int64(req.NFFT)) / float64(maxFrames-1)))
			if hop < 1 {
				hop = 1
			}
			note = fmt.Sprintf("hop widened to %d to bound frame count (requested overlap kept for window shape)", hop)
		}
	}
	cols := MaxCols
	if frames < cols {
		cols = frames
	}
	rows := req.NFFT
	if rows > MaxRows {
		rows = MaxRows
	}
	return framePlan{
		spanSamples: span,
		hop:         hop,
		frames:      frames,
		rows:        rows,
		cols:        cols,
		note:        note,
	}
}

// bandRows crops the rendered band to freqSpan (§19.2) and returns
// the absolute band edges after clamping to the recording band.
// The FFT-frame methods use the bin range; cwt-morlet clamps the
// pseudo-frequency range directly.
func bandEdges(req Request, rec *db.Recording) (loHz, hiHz float64) {
	fs := float64(rec.SampleRate)
	lo := -fs / 2
	hi := fs / 2
	if req.FreqSpan != nil {
		f0 := float64(req.FreqSpan[0]) - float64(rec.CenterFreq)
		f1 := float64(req.FreqSpan[1]) - float64(rec.CenterFreq)
		if f0 > lo {
			lo = f0
		}
		if f1 < hi {
			hi = f1
		}
		if hi < lo {
			lo, hi = 0, 0
		}
	}
	return float64(rec.CenterFreq) + lo, float64(rec.CenterFreq) + hi
}

// Compute validates the request against the recording and runs the
// selected method (§19.2), streaming the .iq file (§10.5/§11.3). It
// is the engine behind the §19.3 endpoint — synchronous, on demand,
// bounded in memory and time by the §19.5 caps and the output grid.
func Compute(rec *db.Recording, req0 Request, limits Limits) (*Result, error) {
	req, err := req0.validate(limits, rec.DurationS)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(rec.FilePath)
	if err != nil {
		return nil, fmt.Errorf("tfr: open %s: %w", rec.FilePath, err)
	}
	defer f.Close()

	fs := float64(rec.SampleRate)
	if fs <= 0 {
		return nil, fmt.Errorf("tfr: recording sample rate %d invalid", rec.SampleRate)
	}
	startSample := int64(math.Floor(req.T0 * fs))
	spanSamples := int64(math.Ceil((req.T1 - req.T0) * fs))
	if avail := int64(rec.SizeBytes/4) - startSample; spanSamples > avail {
		spanSamples = avail // file shorter than the row says: render what exists
	}
	if spanSamples < 1 {
		return nil, fmt.Errorf("tfr: empty span")
	}

	res := &Result{
		RecordingID: rec.ID,
		Method:      req.Method,
		T0:          req.T0,
		T1:          req.T1,
		NFFT:        req.NFFT,
		SampleRate:  uint32(rec.SampleRate),
		CenterFreq:  rec.CenterFreq,
		DBStep:      1.0,
		Artifact:    Artifact(req.Method),
	}
	res.FreqLoHz, res.FreqHiHz = bandEdges(req, rec)

	switch req.Method {
	case MethodStft:
		res.Window = req.WindowVal()
		err = stftCompute(f, rec, req, startSample, spanSamples, res)
	case MethodReassigned:
		res.Window = req.WindowVal()
		err = reassignCompute(f, rec, req, startSample, spanSamples, res)
	case MethodSpwvd:
		err = spwvdCompute(f, rec, req, startSample, spanSamples, res)
	case MethodCwtMorlet:
		err = cwtCompute(f, rec, req, startSample, spanSamples, res)
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}
