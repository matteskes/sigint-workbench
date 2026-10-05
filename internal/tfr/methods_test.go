package tfr

import (
	"math"
	"testing"
)

func TestReassignedTone(t *testing.T) {
	const (
		fs      = 1024.0
		nfft    = 256
		toneBin = 32
	)
	rec := writeIQ(t, toneIQ(fs, int(fs), toneBin*fs/nfft), uint32(fs))
	res, err := Compute(rec, Request{
		Method: MethodReassigned, NFFT: nfft, T1: 1, Window: WindowGaussian,
	}, engineLimits())
	if err != nil {
		t.Fatal(err)
	}
	wantRow := (toneBin + nfft/2) * res.Rows / nfft
	if row := peakRow(res); math.Abs(float64(row-wantRow)) > 2 {
		t.Errorf("peak row %d, want ≈%d", row, wantRow)
	}
	if res.Artifact != Artifact(MethodReassigned) {
		t.Errorf("artifact note missing/wrong: %q", res.Artifact)
	}
}

func TestSpwvdTone(t *testing.T) {
	const (
		fs      = 1024.0
		nfft    = 256
		toneBin = 32
	)
	rec := writeIQ(t, toneIQ(fs, int(fs), toneBin*fs/nfft), uint32(fs))
	res, err := Compute(rec, Request{Method: MethodSpwvd, NFFT: nfft, T1: 1}, engineLimits())
	if err != nil {
		t.Fatal(err)
	}
	// Integer-lag WVD: usable band ±fs/4 at df = fs/(4nfft); a tone at
	// fs/8 sits three quarters of the way up the band.
	frac := (toneBin*fs/nfft + fs/4) / (fs / 2)
	wantRow := int(frac * float64(res.Rows))
	if row := peakRow(res); math.Abs(float64(row-wantRow)) > 3 {
		t.Errorf("peak row %d, want ≈%d", row, wantRow)
	}
	if res.FreqHiHz-res.FreqLoHz > fs/2+1 {
		t.Errorf("SPWVD band wider than ±fs/4: %g..%g", res.FreqLoHz, res.FreqHiHz)
	}
}

func TestCWTTone(t *testing.T) {
	const (
		fs       = 1024.0
		nfft     = 256
		toneFreq = 32.0 // Hz offset — mid of [fs/nfft=4, fs/4=256] log band
	)
	rec := writeIQ(t, toneIQ(fs, int(fs), toneFreq), uint32(fs))
	res, err := Compute(rec, Request{Method: MethodCwtMorlet, NFFT: nfft, T1: 1}, engineLimits())
	if err != nil {
		t.Fatal(err)
	}
	// Log-spaced rows: expected fractional position of 32 Hz in [4, 256].
	frac := math.Log(toneFreq/(fs/nfft)) / math.Log((fs/4)/(fs/nfft))
	wantRow := int((1 - frac) * float64(res.Rows-1)) // row 0 = fLo
	if row := peakRow(res); math.Abs(float64(row-wantRow)) > 2 {
		t.Errorf("peak row %d, want ≈%d", row, wantRow)
	}
	if res.FreqLoHz-float64(rec.CenterFreq) < fs/nfft-1 {
		t.Errorf("band floor below Morlet limit: %g", res.FreqLoHz)
	}
}

func TestGridBoundsAndHopWidening(t *testing.T) {
	// 30 k samples with the frame cap pulled down to 100 — the engine
	// must widen the hop and keep the grid bounded.
	old := frameCap
	frameCap = 100
	defer func() { frameCap = old }()

	const fs = 1000.0
	rec := writeIQ(t, toneIQ(fs, int(30*fs), 100), uint32(fs))
	res, err := Compute(rec, Request{
		Method: MethodStft, NFFT: 256, T1: 30,
	}, engineLimits())
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows > MaxRows || res.Cols > MaxCols {
		t.Fatalf("grid exceeded bounds: %dx%d", res.Rows, res.Cols)
	}
	if res.Note == "" {
		t.Error("expected a hop-widening note when the frame cap binds")
	}
}

func TestSpanClampedToAvailableSamples(t *testing.T) {
	// The row claims 2 s of audio; the file holds 0.5 s. The engine
	// renders what exists instead of erroring (finalized-truncated
	// rows must stay analyzable).
	const fs = 1024.0
	rec := writeIQ(t, toneIQ(fs, int(fs/2), 32), uint32(fs))
	rec.DurationS = 2
	res, err := Compute(rec, Request{Method: MethodStft, NFFT: 256, T0: 0.4, T1: 0.6}, engineLimits())
	if err != nil {
		t.Fatalf("truncated file should render, got: %v", err)
	}
	nonZero := 0
	for _, v := range res.Tile {
		if v > -128 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("render is entirely floor despite real samples in span")
	}
}

func TestArtifactCoversEveryMethod(t *testing.T) {
	// §19.2: every response must name the method's dominant artifact —
	// so every shipped method must have one.
	for _, m := range []string{MethodStft, MethodReassigned, MethodSpwvd, MethodCwtMorlet} {
		if Artifact(m) == "" {
			t.Errorf("method %s has no artifact note", m)
		}
	}
}
