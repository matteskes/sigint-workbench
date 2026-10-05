package dsp

import "testing"

func TestMaxPoolWrappedGroupCount(t *testing.T) {
	// Flat −100 dB floor: every group's max must be −100, and the
	// output length must equal the group count.
	in := make([]float64, 64)
	for i := range in {
		in[i] = -100
	}
	out := MaxPoolWrapped(in, 4)
	if len(out) != 4 {
		t.Fatalf("len = %d, want 4", len(out))
	}
	for i, v := range out {
		if v != -100 {
			t.Fatalf("out[%d] = %v, want -100", i, v)
		}
	}
}

func TestMaxPoolWrappedOrdering(t *testing.T) {
	// n=16, groups=4 (4 bins/group). Wrapped bins 0..7 are the
	// positive offsets and 8..15 the negative ones (−fs/2..−df), so
	// after the fftshift mapping the lowest frequencies must land in
	// output group 0 and the highest in the last group. Background is
	// a −100 dB floor; tones rise above it.
	in := make([]float64, 16)
	for i := range in {
		in[i] = -100
	}
	// Tone at wrapped 12 (−4·df) → shifted k=4 → group 1.
	in[12] = -10
	// DC spike (wrapped 0) → shifted k=8 → group 2.
	in[0] = -20
	// Highest positive bin (wrapped 7, +7·df) → shifted k=15 → group 3.
	in[7] = -30
	out := MaxPoolWrapped(in, 4)
	want := []float64{-100, -10, -20, -30}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("out[%d] = %v, want %v (fftshift ordering broken)", i, out[i], want[i])
		}
	}
}

func TestMaxPoolWrappedPerGroupMax(t *testing.T) {
	// Two peaks in different groups: each group must report its own
	// member's max (group i = max of member bins, §18.1).
	in := make([]float64, 8)
	for i := range in {
		in[i] = -100
	}
	in[2] = -5 // wrapped 2 → shifted k=6 → group 1
	in[6] = -7 // wrapped 6 → shifted k=2 → group 0
	out := MaxPoolWrapped(in, 2)
	if out[0] != -7 || out[1] != -5 {
		t.Fatalf("out = %v, want [-7 -5]", out)
	}
	// A loud bin must dominate its group even next to quiet neighbors.
	in2 := make([]float64, 8)
	for i := range in2 {
		in2[i] = -100
	}
	in2[5] = -3
	out2 := MaxPoolWrapped(in2, 2)
	if out2[0] != -3 || out2[1] != -100 {
		t.Fatalf("out = %v, want [-3 -100]", out2)
	}
}

func TestMaxPoolWrappedRejectsInvalid(t *testing.T) {
	if got := MaxPoolWrapped(nil, 4); got != nil {
		t.Fatalf("empty input: got %v, want nil", got)
	}
	if got := MaxPoolWrapped(make([]float64, 10), 4); got != nil {
		t.Fatalf("non-divisible: got %v, want nil", got)
	}
	if got := MaxPoolWrapped(make([]float64, 16), 0); got != nil {
		t.Fatalf("zero groups: got %v, want nil", got)
	}
	if got := MaxPoolWrapped(make([]float64, 16), -2); got != nil {
		t.Fatalf("negative groups: got %v, want nil", got)
	}
}
