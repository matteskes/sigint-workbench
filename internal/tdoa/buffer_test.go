package tdoa

import (
	"math"
	"math/cmplx"
	"testing"
	"time"
)

func toneRun(id string, t0 time.Time, fs float64, n int, f float64) Run {
	w := make([]complex128, n)
	for k := range w {
		w[k] = cmplx.Exp(complex(0, 2*math.Pi*f*float64(k)/fs))
	}
	return Run{ReceiverID: id, AnchorUTC: t0, SampleRate: fs, Samples: w}
}

func TestBufferWindowIntersectionAndGap(t *testing.T) {
	t0 := time.Unix(1700000000, 0).UTC()
	b := NewBuffer(500 * time.Millisecond)
	b.Add(toneRun("a", t0, 2.4e6, 24000, 50e3))
	// Receiver b has a gap: [0, 4 ms) + [6, 10 ms).
	b.Add(toneRun("b", t0, 2.4e6, 9600, 50e3))
	b.Add(toneRun("b", t0.Add(6*time.Millisecond), 2.4e6, 9600, 50e3))

	w, got, ok := b.Window([]string{"a", "b"}, 3500*time.Microsecond, 2.4e6)
	if !ok {
		t.Fatal("window should fit in the common coverage above the gap")
	}
	if want := t0.Add(6500 * time.Microsecond); !got.Equal(want) {
		t.Fatalf("window start = %v, want %v (latest covered span)",
			got, want)
	}
	if len(w["a"].Samples) != 8400 || len(w["b"].Samples) != 8400 {
		t.Fatalf("window lengths = %d/%d, want 8400/8400",
			len(w["a"].Samples), len(w["b"].Samples))
	}
	if _, _, ok := b.Window([]string{"a", "b"}, 8*time.Millisecond, 2.4e6); ok {
		t.Fatal("8 ms must fail: no single gap-free run covers it on b")
	}
}

func TestBufferHorizonPrune(t *testing.T) {
	t0 := time.Unix(1700000000, 0).UTC()
	b := NewBuffer(50 * time.Millisecond)
	b.Add(toneRun("a", t0, 2.4e6, 24000, 50e3))
	b.Add(toneRun("a", t0.Add(200*time.Millisecond), 2.4e6, 24000, 50e3))
	if len(b.runs["a"]) != 1 {
		t.Fatalf("runs kept = %d, want 1 (stale run pruned)", len(b.runs["a"]))
	}
	if _, _, ok := b.Window([]string{"a"}, 10*time.Millisecond, 2.4e6); !ok {
		t.Fatal("recent run must still serve a window")
	}
}

func TestBufferMixedRateWindow(t *testing.T) {
	t0 := time.Unix(1700000000, 0).UTC()
	b := NewBuffer(500 * time.Millisecond)
	b.Add(toneRun("a", t0, 2.4e6, 24000, 50e3))
	b.Add(toneRun("b", t0, 1.8e6, 18000, 50e3))
	w, _, ok := b.Window([]string{"a", "b"}, 10*time.Millisecond, 1.8e6)
	if !ok {
		t.Fatal("mixed-rate window should align at the common rate")
	}
	if len(w["a"].Samples) != 18000 || len(w["b"].Samples) != 18000 {
		t.Fatalf("lengths = %d/%d, want 18000/18000",
			len(w["a"].Samples), len(w["b"].Samples))
	}
}
