package dsp

import (
	"math"
	"testing"
)

func TestParseWindow(t *testing.T) {
	cases := []struct {
		in   string
		want WindowKind
	}{
		{"", WindowRectangular},
		{"rectangular", WindowRectangular},
		{"hann", WindowHann},
		{"hamming", WindowHamming},
		{"blackman", WindowBlackman},
	}
	for _, c := range cases {
		got, err := ParseWindow(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseWindow(%q) = %v, %v; want %v, nil",
				c.in, got, err, c.want)
		}
	}
	if _, err := ParseWindow("chebyshev"); err == nil {
		t.Error("ParseWindow: unknown name must error, got nil")
	}
}

func TestApplyWindowRectangularNoOp(t *testing.T) {
	s := []float64{1, -2, 3, -4, 0.5, -0.25, 0, 0}
	before := append([]float64(nil), s...)
	ApplyWindow(s, WindowRectangular)
	for i := range s {
		if s[i] != before[i] {
			t.Fatalf("rectangular must be a no-op: s[%d] %v != %v",
				i, s[i], before[i])
		}
	}
}

// hann (periodic): w[0] = 0, w[n/2] = 1, symmetric about n/2.
func TestApplyWindowHann(t *testing.T) {
	const n = 8
	s := make([]float64, 2*n)
	for i := range s {
		s[i] = 1
	}
	ApplyWindow(s, WindowHann)
	if s[0] != 0 || s[1] != 0 {
		t.Fatalf("hann must zero the first pair, got %v", s[:2])
	}
	if got := s[2*(n/2)]; math.Abs(got-1) > 1e-12 {
		t.Fatalf("hann w[n/2] = %v, want 1", got)
	}
	for i := 1; i < n; i++ {
		want := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/n))
		if math.Abs(s[2*i]-want) > 1e-12 {
			t.Fatalf("hann w[%d] = %v, want %v", i, s[2*i], want)
		}
		if math.Abs(s[2*i]-s[2*(n-i)]) > 1e-12 {
			t.Fatalf("hann not symmetric at %d: %v vs %v",
				i, s[2*i], s[2*(n-i)])
		}
	}
}

func TestApplyWindowHammingBlackmanEndpoints(t *testing.T) {
	const n = 16
	s := make([]float64, 2*n)
	for i := range s {
		s[i] = 1
	}
	ApplyWindow(s, WindowHamming)
	if math.Abs(s[0]-0.08) > 1e-12 { // 0.54 - 0.46
		t.Fatalf("hamming w[0] = %v, want 0.08", s[0])
	}
	for i := range s {
		s[i] = 1
	}
	ApplyWindow(s, WindowBlackman)
	if math.Abs(s[0]) > 1e-12 { // 0.42 - 0.5 + 0.08
		t.Fatalf("blackman w[0] = %v, want 0", s[0])
	}
}

// Odd sample counts (trailing half-pair) must not panic or index out
// of range; the trailing sample is ignored.
func TestApplyWindowOddLength(t *testing.T) {
	s := []float64{1, 1, 1, 1, 1}
	ApplyWindow(s, WindowHann) // 2 full pairs processed
	if s[0] != 0 || s[4] != 1 {
		t.Fatalf("odd length handling wrong: %v", s)
	}
}
