// Package tfr — on-demand time-frequency analysis over stored raw IQ
// (SPEC §19, D10).
//
// Computation lives in the recorder (§10.5/§11.3: it owns the .iq
// files) and runs only when a client asks — nothing here is
// continuous, and nothing here feeds the §5 detection pipeline, the
// §14 event stream, or the §18 tap (§19.1). §19 output MUST NOT feed
// the dashboard's live waterfall.
//
// The request enters through the api-gateway proxy (§13.1, A3) as
// POST /api/recordings/{id}/tfr and is answered synchronously with
// numeric tiles plus metadata naming the method's dominant artifact
// (§19.2). Errors per §19.3: 400 invalid params, 404 unknown
// recording (or feature disabled), 413 span beyond tfr.max_span_s.
//
// All four methods stream: the .iq file is read window-by-window with
// bounded memory (a few windows), and every frame is max-pooled
// online into a bounded output grid (≤ MaxCols × MaxRows cells,
// int8 dB relative to the tile max) — a 30 s span never materializes
// as a full-rate sample array.
package tfr

import (
	"fmt"
	"math"
)

// Method names (§19.2, normative set).
const (
	MethodStft       = "stft"
	MethodReassigned = "reassigned"
	MethodSpwvd      = "spwvd"
	MethodCwtMorlet  = "cwt-morlet"
)

// STFT window names (§19.2: rectangular | gaussian | hamming).
const (
	WindowRectangular = "rectangular"
	WindowGaussian    = "gaussian"
	WindowHamming     = "hamming"
)

// artifacts maps each method to its dominant artifact note (§19.2:
// every response MUST name it so the UI can surface it beside the
// render, §19.4).
var artifacts = map[string]string{
	MethodStft:       "window resolution trade-off: a sharp window wide in frequency, a wide window sharp in time",
	MethodReassigned: "reassignment smears noise-dominated regions — artifact, not a bug",
	MethodSpwvd:      "kernel smoothing suppresses cross-terms at the cost of resolution; integer-lag WVD folds content beyond ±fs/4 into mirrored ridges",
	MethodCwtMorlet:  "scale smearing: low-frequency rows average over proportionally longer time",
}

// Artifact returns the §19.2 dominant-artifact note for a method.
func Artifact(method string) string { return artifacts[method] }

// Request is one §19.3 POST body. Times are seconds within the
// recording; freqSpan is absolute Hz (recording center ± fs/2).
type Request struct {
	Method   string     `json:"method"`
	T0       float64    `json:"t0"`
	T1       float64    `json:"t1"`
	NFFT     int        `json:"nfft"`
	Overlap  *float64   `json:"overlap"`  // 0 ≤ overlap < 1; default 0.75
	Window   string     `json:"window"`   // stft/reassigned only; default hamming
	FreqSpan *[2]uint64 `json:"freqSpan"` // optional absolute [lo, hi] Hz
}

// Limits carries the §19.5 config caps into validation.
type Limits struct {
	Enabled  bool
	MaxSpanS float64
	MaxNFFT  int
}

// Defaults applied where the spec fixes one (§19.2/§19.5).
const (
	DefaultOverlap = 0.75
	DefaultWindow  = WindowHamming
	MinNFFT        = 256
	DefaultMaxSpan = 30.0
	DefaultMaxNFFT = 16384
)

// OverlapVal returns the effective overlap (default applied).
func (r Request) OverlapVal() float64 {
	if r.Overlap == nil {
		return DefaultOverlap
	}
	return *r.Overlap
}

// WindowVal returns the effective window (default applied).
func (r Request) WindowVal() string {
	if r.Window == "" {
		return DefaultWindow
	}
	return r.Window
}

// spanClampErr marks §19.3's 413 (span beyond max_span_s) so the
// handler can distinguish it from a plain 400.
type spanClampErr struct{ msg string }

func (e spanClampErr) Error() string { return e.msg }

// validate applies §19.2's normative constraints and fills defaults.
// Returned errors carry client-actionable messages (the handler maps
// spanClampErr to 413, everything else to 400).
func (r Request) validate(l Limits, durationS float64) (Request, error) {
	if !l.Enabled {
		// Feature absent, not an error state (§19.3) — the handler
		// turns this into 404 before the body is even read.
		return r, fmt.Errorf("tfr feature disabled")
	}
	switch r.Method {
	case MethodStft, MethodReassigned, MethodSpwvd, MethodCwtMorlet:
	default:
		return r, fmt.Errorf("unknown method %q (want stft|reassigned|spwvd|cwt-morlet)", r.Method)
	}
	maxNFFT := l.MaxNFFT
	if maxNFFT <= 0 {
		maxNFFT = DefaultMaxNFFT
	}
	if r.NFFT < MinNFFT || r.NFFT > maxNFFT {
		return r, fmt.Errorf("nfft %d outside [%d, %d]", r.NFFT, MinNFFT, maxNFFT)
	}
	if o := r.OverlapVal(); o < 0 || o >= 1 {
		return r, fmt.Errorf("overlap %g outside [0, 1)", o)
	}
	if r.Method == MethodStft || r.Method == MethodReassigned {
		switch w := r.WindowVal(); w {
		case WindowRectangular, WindowGaussian, WindowHamming:
		default:
			return r, fmt.Errorf("unknown window %q (want rectangular|gaussian|hamming)", w)
		}
		// Reassignment reads the window's phase gradient (Kodera/
		// Flandrin); a rectangular window has none in its interior,
		// so the estimates collapse.
		if r.Method == MethodReassigned && r.WindowVal() == WindowRectangular {
			return r, fmt.Errorf("reassigned requires gaussian or hamming (rectangular has no phase gradient)")
		}
	}
	// Span order and bounds. A zero t1 defaults to the head of the
	// recording clamped to the span cap — the common "analyze the
	// start" case stays a one-liner for clients.
	if r.T1 == 0 {
		r.T1 = math.Min(durationS, l.MaxSpanS)
	}
	if r.T1 <= 0 {
		return r, fmt.Errorf("recording has zero duration")
	}
	if r.T0 < 0 {
		return r, fmt.Errorf("t0 %g negative", r.T0)
	}
	if r.T1 <= r.T0 {
		return r, fmt.Errorf("t1 %g must exceed t0 %g", r.T1, r.T0)
	}
	if r.T1 > durationS+1e-9 {
		return r, fmt.Errorf("t1 %g beyond recording duration %g", r.T1, durationS)
	}
	if r.T1-r.T0 > l.MaxSpanS+1e-9 {
		return r, spanClampErr{fmt.Sprintf("span %g s exceeds tfr.max_span_s %g", r.T1-r.T0, l.MaxSpanS)}
	}
	return r, nil
}

// Output grid bounds: every render is max-pooled online into at most
// MaxCols time columns × MaxRows frequency rows, so response size and
// compute stay bounded regardless of span/rate (§19.3 synchronous).
const (
	MaxCols = 512
	MaxRows = 512
	// MaxFrames is the default frame-count cap; a pathological overlap
	// (hop → 0) widens the effective hop instead of exploding. The
	// realigned values are reported in the response metadata.
	MaxFrames = 200000
)

// frameCap is the live frame-count bound (a package var so tests can
// exercise the widening path with tiny fixtures).
var frameCap = MaxFrames
