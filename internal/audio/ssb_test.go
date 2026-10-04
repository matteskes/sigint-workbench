package audio

import (
	"math"
	"math/cmplx"
	"testing"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	ssbTestSR  = 48000
	ssbTestN   = 8192
	ssbTestBin = 150
)

// ssbTestToneHz is chosen to land exactly on DFT bin 150 so the
// sideband filter treats it orthogonally (no spectral leakage).
var ssbTestToneHz = float64(ssbTestBin) * ssbTestSR / float64(ssbTestN) // 878.90625 Hz

// ssbTone generates a full-scale*0.8 complex tone at +ssbTestToneHz
// (positive) or -ssbTestToneHz (negative) offset from zero IF.
func ssbTone(positive bool) []complex64 {
	iq := make([]complex64, ssbTestN)
	for i := range iq {
		ph := 2 * math.Pi * ssbTestToneHz * float64(i) / ssbTestSR
		if !positive {
			ph = -ph
		}
		iq[i] = complex64(0.8 * complex(math.Cos(ph), math.Sin(ph)))
	}
	return iq
}

// dominantFreq returns the strongest positive-frequency component of
// the signal in Hz (DC ignored).
func dominantFreq(t *testing.T, x []float32, sampleRate float64) float64 {
	t.Helper()
	buf := make([]complex128, len(x))
	for i, v := range x {
		buf[i] = complex(float64(v), 0)
	}
	fft := fourier.NewCmplxFFT(len(x))
	coeffs := fft.Coefficients(nil, buf)
	bestK, bestMag := 0, 0.0
	for k := 1; k < len(coeffs)/2; k++ {
		if m := cmplx.Abs(coeffs[k]); m > bestMag {
			bestMag, bestK = m, k
		}
	}
	return float64(bestK) * sampleRate / float64(len(x))
}

func rms(x []float32) float64 {
	if len(x) == 0 {
		return 0
	}
	var sum float64
	for _, v := range x {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(x)))
}

func rmsF64(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

func TestSSBDemodulate_USB(t *testing.T) {
	d := NewSSBDemodulator(ssbTestSR, "upper")
	out, err := d.Demodulate(ssbTone(true), ssbTestSR)
	if err != nil {
		t.Fatalf("Demodulate: %v", err)
	}
	if len(out) != ssbTestN {
		t.Fatalf("output length = %d, want %d", len(out), ssbTestN)
	}
	if f := dominantFreq(t, out, ssbTestSR); math.Abs(f-ssbTestToneHz) > 30 {
		t.Errorf("USB demod dominant freq = %.1f Hz, want ~%.1f Hz", f, ssbTestToneHz)
	}
}

func TestSSBDemodulate_LSB(t *testing.T) {
	d := NewSSBDemodulator(ssbTestSR, "lower")
	out, err := d.Demodulate(ssbTone(false), ssbTestSR)
	if err != nil {
		t.Fatalf("Demodulate: %v", err)
	}
	if f := dominantFreq(t, out, ssbTestSR); math.Abs(f-ssbTestToneHz) > 30 {
		t.Errorf("LSB demod dominant freq = %.1f Hz, want ~%.1f Hz (negative offset folded to positive audio)", f, ssbTestToneHz)
	}
}

func TestSSBDemodulate_SidebandRejection(t *testing.T) {
	const minRejectionDB = -30.0
	// Assert against the un-normalized filter core: peak normalization
	// rescales any float residue to full scale, which would mask the
	// rejection ratio.
	usbWanted := ssbSelectBand(ssbTone(true), ssbTestSR, true, 3000)
	usbRejected := ssbSelectBand(ssbTone(false), ssbTestSR, true, 3000)
	if db := 20 * math.Log10(rmsF64(usbRejected) / rmsF64(usbWanted)); db > minRejectionDB {
		t.Errorf("USB filter rejects LSB tone at %.1f dB, want <= %.1f dB", db, minRejectionDB)
	}

	lsbWanted := ssbSelectBand(ssbTone(false), ssbTestSR, false, 3000)
	lsbRejected := ssbSelectBand(ssbTone(true), ssbTestSR, false, 3000)
	if db := 20 * math.Log10(rmsF64(lsbRejected) / rmsF64(lsbWanted)); db > minRejectionDB {
		t.Errorf("LSB filter rejects USB tone at %.1f dB, want <= %.1f dB", db, minRejectionDB)
	}
}

func TestSSBDemodulate_ConstantSignal(t *testing.T) {
	d := NewSSBDemodulator(ssbTestSR, "upper")
	iq := make([]complex64, 1024)
	for i := range iq {
		iq[i] = complex(1, 0) // pure DC → dropped with the DC bin
	}
	out, err := d.Demodulate(iq, ssbTestSR)
	if err != nil {
		t.Fatalf("Demodulate: %v", err)
	}
	for i, v := range out {
		if v != 0 {
			t.Fatalf("sample %d = %v, want 0 for constant signal", i, v)
		}
	}
}

func TestSSBDemodulate_Errors(t *testing.T) {
	d := NewSSBDemodulator(ssbTestSR, "upper")
	if _, err := d.Demodulate([]complex64{1, 2, 3}, ssbTestSR); err == nil {
		t.Fatal("expected error for too few samples, got nil")
	}
	if _, err := d.Demodulate(make([]complex64, 16), 0); err == nil {
		t.Fatal("expected error for zero sample rate, got nil")
	}
}

func TestSSBDemodulator_NameAndPair(t *testing.T) {
	usb := NewSSBDemodulator(ssbTestSR, "usb")
	lsb := NewSSBDemodulator(ssbTestSR, "lower")
	if usb.Name() != "USB" {
		t.Errorf("Name() = %q, want USB", usb.Name())
	}
	if lsb.Name() != "LSB" {
		t.Errorf("Name() = %q, want LSB", lsb.Name())
	}
	if !usb.CanHandle("SSB") || !usb.CanHandle("lsb") {
		t.Error("CanHandle should accept SSB family strings")
	}
	if usb.CanHandle("FM") {
		t.Error("CanHandle(FM) = true, want false")
	}
	if !usb.CanHandlePair("USB", "") || !usb.CanHandlePair("SSB", "USB") {
		t.Error("USB demod should claim (USB,\"\") and (SSB,USB)")
	}
	if usb.CanHandlePair("SSB", "LSB") {
		t.Error("USB demod should not claim (SSB,LSB)")
	}
	if !lsb.CanHandlePair("LSB", "") {
		t.Error("LSB demod should claim (LSB,\"\")")
	}
	if lsb.CanHandlePair("USB", "") {
		t.Error("LSB demod should not claim (USB,\"\")")
	}
}

// TestSSBRegistrySelection pins the (modulation, subType) → demod
// mapping of the default registry (§10.1).
func TestSSBRegistrySelection(t *testing.T) {
	r := DefaultRegistry()
	usb, err := r.Get("SSB", "USB")
	if err != nil {
		t.Fatalf("Get(SSB,USB): %v", err)
	}
	if usb.Name() != "USB" {
		t.Errorf("Get(SSB,USB).Name() = %q, want USB", usb.Name())
	}
	lsb, err := r.Get("SSB", "LSB")
	if err != nil {
		t.Fatalf("Get(SSB,LSB): %v", err)
	}
	if lsb.Name() != "LSB" {
		t.Errorf("Get(SSB,LSB).Name() = %q, want LSB", lsb.Name())
	}
	// An unrecognized subtype falls back to family order: SSB → upper.
	bogus, err := r.Get("SSB", "BOGUS")
	if err != nil {
		t.Fatalf("Get(SSB,BOGUS): %v", err)
	}
	if bogus.Name() != "USB" {
		t.Errorf("Get(SSB,BOGUS).Name() = %q, want USB (family fallback)", bogus.Name())
	}
}