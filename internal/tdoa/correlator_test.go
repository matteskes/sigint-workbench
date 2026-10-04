package tdoa

import (
	"math"
	"testing"
)

// testTones is a fixed deterministic multitone for correlator tests.
// Spacings and phases are deliberately irregular: uniform spacing
// with linear phase makes the waveform a pulse comb whose
// correlation has ambiguous periodic peaks.
func testTones() []simTone {
	f := [8]float64{31e3, 47e3, 66.5e3, 82e3, 99e3, 118e3, 137e3, 152e3}
	p := [8]float64{0.0, 1.1, 2.3, 0.7, 2.9, 1.9, 0.4, 2.5}
	tones := make([]simTone, 8)
	for k := range tones {
		tones[k] = simTone{f: f[k], amp: 0.7, phase: p[k]}
	}
	return tones
}

// windowAt synthesizes one receiver's window observing the shared
// waveform delayed by delayS (§9.5 step 2 model).
func windowAt(fs float64, n int, delayS float64, tones []simTone) Window {
	w := make([]complex128, n)
	for k := range w {
		w[k] = simWaveform(float64(k)/fs-delayS, tones)
	}
	return Window{SampleRate: fs, Samples: w}
}

// TestDelaySignAndSubSample pins the τᵢⱼ sign convention (§9.5:
// positive when the emitter is farther from i) and the parabolic
// sub-sample refinement: b is delayed 3.4 samples behind a, so
// τ_ab = τa − τb must come out negative and land on the fraction.
func TestDelaySignAndSubSample(t *testing.T) {
	fs := 2.4e6
	tones := testTones()
	a := windowAt(fs, 24000, 0, tones)
	b := windowAt(fs, 24000, 3.4/fs, tones)
	want := -3.4 / fs * 1e9 // ns
	d, ok := Delay(a, b, 20000)
	if !ok {
		t.Fatal("Delay failed")
	}
	if d.TauNS >= 0 {
		t.Fatalf("sign convention broken: TauNS = %.3f ns, want < 0", d.TauNS)
	}
	if math.Abs(d.TauNS-want) > 1/fs*1e9 { // within 1 sample
		t.Fatalf("TauNS = %.3f ns, want %.3f ns", d.TauNS, want)
	}
	// The reverse pair flips the sign at the same magnitude.
	d2, ok := Delay(b, a, 20000)
	if !ok {
		t.Fatal("Delay failed on reverse pair")
	}
	if math.Abs(d2.TauNS+want) > 1/fs*1e9 {
		t.Fatalf("reverse TauNS = %.3f ns, want %.3f ns", d2.TauNS, -want)
	}
	if d.Peak < 0.9 {
		t.Fatalf("clean-signal peak = %.3f, want ≈ 1", d.Peak)
	}
}
