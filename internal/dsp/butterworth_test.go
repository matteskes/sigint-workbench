package dsp

import (
	"math"
	"testing"
)

// steadyStateAmplitude filters a sine and returns the RMS amplitude of
// the output after discarding `settle` transient samples.
func steadyStateAmplitude(t *testing.T, f0, fs float64, n, settle int, sections []Biquad) float64 {
	t.Helper()
	sec := make([]Biquad, len(sections))
	copy(sec, sections) // fresh state
	sum := 0.0
	m := 0
	for i := 0; i < n; i++ {
		x := math.Sin(2 * math.Pi * f0 * float64(i) / fs)
		y := float64(x)
		for j := range sec {
			y = sec[j].Process(y)
		}
		if i >= settle {
			sum += y * y
			m++
		}
	}
	return math.Sqrt(sum / float64(m))
}

// unitSineRMS is the RMS of the unit-amplitude sine used in tests.
const unitSineRMS = 1.0 / math.Sqrt2

func dbGain(in, out float64) float64 {
	if in <= 0 || out <= 0 {
		return -300
	}
	return 20 * math.Log10(out/in)
}

func TestButterworthLowpassOrder4(t *testing.T) {
	const fs = 44100.0
	sections := Butterworth(Lowpass, 1000, 0, fs, 4)
	if len(sections) != 2 {
		t.Fatalf("order 4 → %d sections, want 2", len(sections))
	}

	// Passband: 100 Hz ≈ 0 dB
	gainPass := dbGain(unitSineRMS, steadyStateAmplitude(t, 100, fs, 20000, 2000, sections))
	if math.Abs(gainPass) > 0.5 {
		t.Fatalf("passband gain = %.2f dB, want ~0 dB", gainPass)
	}

	// Stopband: 10 kHz should be well below -30 dB for a 4th order LP.
	sec2 := make([]Biquad, len(sections))
	copy(sec2, sections)
	gainStop := dbGain(unitSineRMS, steadyStateAmplitude(t, 10000, fs, 20000, 2000, sec2))
	if gainStop > -30 {
		t.Fatalf("stopband gain = %.2f dB, want < -30 dB", gainStop)
	}
}

func TestButterworthOddOrder(t *testing.T) {
	const fs = 48000.0
	sections := Butterworth(Lowpass, 4000, 0, fs, 3)
	if len(sections) != 2 {
		t.Fatalf("order 3 → %d sections, want 2 (one 1st-order + one 2nd-order)", len(sections))
	}
	gainPass := dbGain(unitSineRMS, steadyStateAmplitude(t, 500, fs, 20000, 2000, sections))
	if math.Abs(gainPass) > 0.5 {
		t.Fatalf("passband gain = %.2f dB, want ~0 dB", gainPass)
	}
}

func TestButterworthHighpassBlocksDC(t *testing.T) {
	const fs = 48000.0
	sections := Butterworth(Highpass, 2000, 0, fs, 2)
	out := make([]float64, 0, 10000)
	sec := make([]Biquad, len(sections))
	copy(sec, sections)
	for i := 0; i < 10000; i++ {
		y := float64(1.0) // step input = DC
		for j := range sec {
			y = sec[j].Process(y)
		}
		out = append(out, y)
	}
	// DC gain must decay to ~0.
	last := math.Abs(out[len(out)-1])
	if last > 1e-3 {
		t.Fatalf("steady-state DC output = %g, want ~0", last)
	}

	// Passband: 8 kHz ≈ 0 dB.
	gainPass := dbGain(unitSineRMS, steadyStateAmplitude(t, 8000, fs, 20000, 2000, sections))
	if math.Abs(gainPass) > 0.5 {
		t.Fatalf("passband gain = %.2f dB, want ~0 dB", gainPass)
	}
}

func TestButterworthBandpass(t *testing.T) {
	const fs = 48000.0
	f0 := 12000.0
	sections := Butterworth(Bandpass, f0, 10, fs, 1) // Q=10 → ~1.2 kHz BW

	gainCenter := dbGain(unitSineRMS, steadyStateAmplitude(t, f0, fs, 20000, 2000, sections))
	if math.Abs(gainCenter) > 0.5 {
		t.Fatalf("center gain = %.2f dB, want ~0 dB", gainCenter)
	}

	sec2 := make([]Biquad, len(sections))
	copy(sec2, sections)
	gainAway := dbGain(unitSineRMS, steadyStateAmplitude(t, f0/4, fs, 20000, 2000, sec2))
	if gainAway > -20 {
		t.Fatalf("off-center gain = %.2f dB, want < -20 dB", gainAway)
	}
}

func TestFilterBiquadsEdgeCases(t *testing.T) {
	if out := FilterBiquads(nil, nil); out != nil {
		t.Fatal("expected nil for empty input")
	}
	in := []float64{1, 2, 3}
	if out := FilterBiquads(in, nil); len(out) != 3 {
		t.Fatalf("empty cascade should pass through, got %d samples", len(out))
	}
	out := FilterBiquads(in, Butterworth(Lowpass, 4000, 0, 48000, 2))
	if len(out) != 3 {
		t.Fatalf("output length = %d, want 3", len(out))
	}
}

func TestDesignSecondOrderInvalidParams(t *testing.T) {
	for _, fs := range []float64{0, -1} {
		for _, f0 := range []float64{0, -100} {
			for kind := FilterKind(0); kind <= Bandpass; kind++ {
				b := designSecondOrder(kind, f0, 1, fs)
				if b.b0 != 1 || b.b1 != 0 || b.b2 != 0 || b.a1 != 0 || b.a2 != 0 {
					t.Fatalf("kind=%d f0=%v fs=%v: expected identity, got %+v", kind, f0, fs, b)
				}
			}
		}
	}
}