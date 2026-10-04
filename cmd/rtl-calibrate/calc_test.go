package main

import (
	"math"
	"testing"
)

func TestImpliedOffsetRoundTrip(t *testing.T) {
	// offset = expected − mean + gain; power_dbm = power_db − gain +
	// offset must then reproduce expected for ANY power_db reading.
	const expected, mean, gain = -40.0, -8.42, 40.0
	off := ImpliedOffset(expected, mean, gain)
	if math.Abs(off-8.42) > 1e-9 {
		t.Fatalf("ImpliedOffset = %v, want 8.42", off)
	}
	if got := mean - gain + off; math.Abs(got-expected) > 1e-9 {
		t.Fatalf("round trip: got %v, want %v", got, expected)
	}
}

func TestPeakPowerDBSkipsDCGuard(t *testing.T) {
	// 16 bins at 1 kHz spacing, wrapped (bins 8..15 = negative
	// offsets −8..−1 kHz). The DC spike must lose to the guard band.
	power := make([]float64, 16)
	freqs := make([]float64, 16)
	for i := range freqs {
		freqs[i] = float64(i) * 1000
		if i >= 8 {
			freqs[i] = float64(i-16) * 1000
		}
	}
	power[0] = 20  // DC spike — excluded
	power[1] = 15  // +1 kHz, inside the guard — excluded
	power[15] = 15 // −1 kHz, inside the guard — excluded
	power[5] = 3
	power[12] = 7 // strongest bin outside the guard

	peak, bin, ok := PeakPowerDB(power, freqs, 2500)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if bin != 12 || peak != 7 {
		t.Fatalf("got bin %d peak %v, want bin 12 peak 7", bin, peak)
	}

	// Guard disabled: the DC spike wins.
	peak, bin, ok = PeakPowerDB(power, freqs, 0)
	if !ok || bin != 0 || peak != 20 {
		t.Fatalf("guard 0: got bin %d peak %v ok %v, want bin 0 peak 20",
			bin, peak, ok)
	}
}

func TestPeakPowerDBAllExcluded(t *testing.T) {
	power := []float64{1, 2, 3}
	freqs := []float64{0, 500, 1000}
	if _, _, ok := PeakPowerDB(power, freqs, 2000); ok {
		t.Fatal("ok = true, want false (all bins inside the guard)")
	}
	if _, _, ok := PeakPowerDB(nil, nil, 0); ok {
		t.Fatal("ok = true, want false (empty spectrum)")
	}
}

func TestMeanStdDev(t *testing.T) {
	xs := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	if got := Mean(xs); got != 5 {
		t.Fatalf("Mean = %v, want 5", got)
	}
	if got := StdDev(xs); math.Abs(got-2) > 1e-12 {
		t.Fatalf("StdDev = %v, want 2", got)
	}
	if got := Mean(nil); got != 0 {
		t.Fatalf("Mean(nil) = %v, want 0", got)
	}
	if got := StdDev([]float64{4}); got != 0 {
		t.Fatalf("StdDev single = %v, want 0", got)
	}
}
