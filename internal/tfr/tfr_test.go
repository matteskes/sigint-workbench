package tfr

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"sigint-workbench/internal/db"
)

// toneIQ synthesizes n samples of a unit complex tone at offsetHz.
func toneIQ(fs float64, n int, offsetHz float64) []complex128 {
	out := make([]complex128, n)
	for i := range out {
		ph := 2 * math.Pi * offsetHz * float64(i) / fs
		out[i] = complex(math.Cos(ph), math.Sin(ph))
	}
	return out
}

// writeIQ fixtures an int16-LE .iq file (§10.5 layout) and returns a
// recording row pointing at it.
func writeIQ(t *testing.T, samples []complex128, fs uint32) *db.Recording {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.iq")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		re := int16(math.Round(real(s) * 32000))
		im := int16(math.Round(imag(s) * 32000))
		b := []byte{byte(re), byte(re >> 8), byte(im), byte(im >> 8)}
		if _, err := f.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	dur := float64(len(samples)) / float64(fs)
	return &db.Recording{
		ID:         "11111111-1111-1111-1111-111111111111",
		FilePath:   path,
		FileFormat: "iq",
		SampleRate: int32(fs),
		CenterFreq: 146_000_000,
		DurationS:  dur,
		SizeBytes:  int64(len(samples)) * 4,
	}
}

// peakRow returns the tile row holding the global maximum.
func peakRow(res *Result) int {
	best := math.Inf(-1)
	row := 0
	for i, v := range res.Tile {
		if float64(v) > best {
			best = float64(v)
			row = i / res.Cols
		}
	}
	return row
}

// engineLimits is the common enabled test config.
func engineLimits() Limits {
	return Limits{Enabled: true, MaxSpanS: 30, MaxNFFT: 16384}
}

func TestValidate(t *testing.T) {
	rec := writeIQ(t, toneIQ(1024, 1024, 32), 1024) // 1 s fixture
	base := Request{Method: MethodStft, T0: 0, T1: 1, NFFT: 256}

	if _, err := base.validate(Limits{Enabled: false}, rec.DurationS); err == nil {
		t.Error("disabled must reject (handler maps to 404)")
	}

	cases := []struct {
		name    string
		mutate  func(r *Request)
		want413 bool
	}{
		{"unknown method", func(r *Request) { r.Method = "wavelet-x" }, false},
		{"nfft below floor", func(r *Request) { r.NFFT = 100 }, false},
		{"nfft above cap", func(r *Request) { r.NFFT = 32768 }, false},
		{"overlap 1.0", func(r *Request) { o := 1.0; r.Overlap = &o }, false},
		{"overlap negative", func(r *Request) { o := -0.1; r.Overlap = &o }, false},
		{"unknown window", func(r *Request) { r.Window = "kaiser" }, false},
		{"t1 before t0", func(r *Request) { r.T0, r.T1 = 2, 1 }, false},
		{"t1 beyond duration", func(r *Request) { r.T1 = 9 }, false},
	}
	for _, tc := range cases {
		req := base
		tc.mutate(&req)
		_, err := req.validate(engineLimits(), rec.DurationS)
		if err == nil {
			t.Errorf("%s: want error", tc.name)
			continue
		}
		var se interface{ HTTPStatus() int }
		got413 := errors.As(err, &se) && se.HTTPStatus() == 413
		if got413 != tc.want413 {
			t.Errorf("%s: 413=%v want %v (%v)", tc.name, got413, tc.want413, err)
		}
	}

	// Defaults: overlap 0.75, window hamming, t1 = duration.
	got, err := (Request{Method: MethodStft, NFFT: 256}).validate(engineLimits(), rec.DurationS)
	if err != nil {
		t.Fatal(err)
	}
	if got.OverlapVal() != DefaultOverlap || got.WindowVal() != DefaultWindow {
		t.Errorf("defaults: overlap=%g window=%s", got.OverlapVal(), got.WindowVal())
	}
	if got.T1 != 1 {
		t.Errorf("t1 default = %g, want 1 (duration)", got.T1)
	}

	// Reassignment rejects rectangular (no phase gradient).
	req := Request{Method: MethodReassigned, NFFT: 256, Window: WindowRectangular}
	if _, err := req.validate(engineLimits(), rec.DurationS); err == nil {
		t.Error("reassigned+rectangular must reject")
	}
	for _, w := range []string{WindowGaussian, WindowHamming} {
		req.Window = w
		if _, err := req.validate(engineLimits(), rec.DurationS); err != nil {
			t.Errorf("reassigned+%s: %v", w, err)
		}
	}
}

func TestValidateSpanCapIs413(t *testing.T) {
	// 413 is for a span that fits the recording but exceeds the
	// tfr.max_span_s cap (§19.3); a span beyond the recording is a 400.
	long := writeIQ(t, toneIQ(1000, 31000, 100), 1000) // 31 s
	req := Request{Method: MethodStft, NFFT: 256, T0: 0, T1: 31}
	_, err := req.validate(engineLimits(), long.DurationS)
	var se interface{ HTTPStatus() int }
	if !errors.As(err, &se) || se.HTTPStatus() != 413 {
		t.Errorf("span 31 s with cap 30: want 413, got %v", err)
	}
}

func TestSTFTToneLandsOnRightRow(t *testing.T) {
	const (
		fs      = 1024.0
		nfft    = 256
		toneBin = 32 // fs/8
	)
	rec := writeIQ(t, toneIQ(fs, int(fs), toneBin*fs/nfft), uint32(fs))
	res, err := Compute(rec, Request{Method: MethodStft, NFFT: nfft, T1: 1}, engineLimits())
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows*res.Cols != len(res.Tile) || len(res.Tile) == 0 {
		t.Fatalf("tile geometry rows=%d cols=%d len=%d", res.Rows, res.Cols, len(res.Tile))
	}
	if res.Rows > MaxRows || res.Cols > MaxCols {
		t.Fatalf("grid bounds: %dx%d", res.Rows, res.Cols)
	}
	// Shifted bin = 32 + 128 = 160 of 256; rows = 256 (full band, ≤512).
	wantRow := (toneBin + nfft/2) * res.Rows / nfft
	if row := peakRow(res); math.Abs(float64(row-wantRow)) > 2 {
		t.Errorf("peak row %d, want ≈%d (tone bin %d)", row, wantRow, toneBin)
	}
	for _, v := range res.Tile {
		if v > 0 || v < -128 {
			t.Fatalf("tile value out of [−128, 0]: %d", v)
		}
	}
	if res.DBRef <= -300 || res.DBRef > 100 {
		t.Errorf("dbRef implausible: %g", res.DBRef)
	}
	if res.Artifact == "" {
		t.Error("§19.2: artifact metadata required")
	}
}

func TestSTFTFreqSpanCrop(t *testing.T) {
	const (
		fs      = 1024.0
		nfft    = 256
		toneBin = 32
	)
	center := uint64(146_000_000)
	rec := writeIQ(t, toneIQ(fs, int(fs), toneBin*fs/nfft), uint32(fs))
	rec.CenterFreq = center
	span := uint64(4 * fs / nfft) // ±4 STFT bins around the tone
	lo := center + uint64(toneBin*fs/nfft) - span/2
	hi := center + uint64(toneBin*fs/nfft) + span/2
	res, err := Compute(rec, Request{
		Method: MethodStft, NFFT: nfft, T1: 1,
		FreqSpan: &[2]uint64{lo, hi},
	}, engineLimits())
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows > 10 {
		t.Errorf("cropped band should render few rows, got %d", res.Rows)
	}
	if res.FreqLoHz < float64(center) || res.FreqHiHz > float64(center)+fs/2 {
		t.Errorf("band edges out of recording band: %g..%g", res.FreqLoHz, res.FreqHiHz)
	}
	if row := peakRow(res); row < res.Rows/3 || row > 2*res.Rows/3 {
		t.Errorf("tone should peak mid-band in a symmetric crop, got row %d/%d", row, res.Rows)
	}
}
