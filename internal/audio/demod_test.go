package audio

import (
	"math"
	"testing"
)

func TestNewAMDemodulator_DefaultRate(t *testing.T) {
	d := NewAMDemodulator(0, "")
	if d.AudioSampleRate() != 48000 {
		t.Fatalf("default audio rate = %d, want 48000", d.AudioSampleRate())
	}
	if got := NewAMDemodulator(24000, "").AudioSampleRate(); got != 24000 {
		t.Fatalf("audio rate = %d, want 24000", got)
	}
}

func TestAMDemodulator_Name(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"", "AM"},
		{"upper", "USB"},
		{"lower", "LSB"},
		{"bogus", "AM"},
	}
	for _, c := range cases {
		if got := NewAMDemodulator(48000, c.mode).Name(); got != c.want {
			t.Errorf("Name(%q) = %q, want %q", c.mode, got, c.want)
		}
	}
}

func TestAMDemodulator_CanHandle(t *testing.T) {
	d := NewAMDemodulator(48000, "")
	for _, mod := range []string{"AM", "DSB", "SSB", "USB", "LSB", "am", "dsb", "ssb", "usb", "lsb"} {
		if !d.CanHandle(mod) {
			t.Errorf("CanHandle(%q) = false, want true", mod)
		}
	}
	for _, mod := range []string{"FM", "WFM", "NFM", "", "AM3"} {
		if d.CanHandle(mod) {
			t.Errorf("CanHandle(%q) = true, want false", mod)
		}
	}
}

func TestAMDemodulate_Errors(t *testing.T) {
	d := NewAMDemodulator(48000, "")
	if _, err := d.Demodulate([]complex64{1, 2, 3}, 48000); err == nil {
		t.Fatal("expected error for too few samples, got nil")
	}
	if _, err := d.Demodulate(make([]complex64, 16), 0); err == nil {
		t.Fatal("expected error for zero sample rate, got nil")
	}
}

func TestAMDemodulate_ConstantSignal(t *testing.T) {
	d := NewAMDemodulator(48000, "")
	iq := make([]complex64, 1024)
	for i := range iq {
		iq[i] = complex(1, 0) // constant envelope → no modulation
	}
	out, err := d.Demodulate(iq, 48000)
	if err != nil {
		t.Fatalf("Demodulate: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
	for i, v := range out {
		if v != 0 {
			t.Fatalf("sample %d = %v, want 0 for constant signal", i, v)
		}
	}
}

func TestAMDemodulate_Tone(t *testing.T) {
	// AM tone: carrier 2000 Hz, modulation 100 Hz, IQ rate 48000 Hz.
	// After envelope detection + DC removal + normalization the audio
	// should be cos(2*pi*100*t) at the input sample rate.
	const (
		sr  = 48000
		fc  = 2000.0
		fa  = 100.0
		n   = sr
		mod = 0.5
	)
	iq := make([]complex64, n)
	for i := range iq {
		t := float64(i) / sr
		env := 1 + mod*math.Cos(2*math.Pi*fa*t)
		// Analytic signal: |iq| == env exactly (no carrier rectification).
		phase := 2 * math.Pi * fc * t
		iq[i] = complex64(complex(env*math.Cos(phase), env*math.Sin(phase)))
	}

	d := NewAMDemodulator(48000, "")
	out, err := d.Demodulate(iq, sr)
	if err != nil {
		t.Fatalf("Demodulate: %v", err)
	}
	if len(out) != n {
		t.Fatalf("output length = %d, want %d", len(out), n)
	}
	// One modulation period is n/fa = 480 samples; peak at t=0, trough at half period.
	const tol = 0.01
	if math.Abs(float64(out[0])-1.0) > tol {
		t.Errorf("out[0] = %v, want ~1.0", out[0])
	}
	half := int(n / (2 * fa))
	if math.Abs(float64(out[half])-(-1.0)) > tol {
		t.Errorf("out[%d] = %v, want ~-1.0", half, out[half])
	}
}

func TestAMDemodulate_Decimation(t *testing.T) {
	d := NewAMDemodulator(48000, "")
	iq := make([]complex64, 960)
	for i := range iq {
		iq[i] = complex(1, 0)
	}
	// IQ rate 96000 → audio rate 48000 → decimate factor 2.
	out, err := d.Demodulate(iq, 96000)
	if err != nil {
		t.Fatalf("Demodulate: %v", err)
	}
	if len(out) != len(iq)/2 {
		t.Fatalf("output length = %d, want %d", len(out), len(iq)/2)
	}
}
func TestNewFMDemodulator_DefaultRate(t *testing.T) {
	f := NewFMDemodulator(25000, 0)
	if f.AudioSampleRate() != 48000 {
		t.Fatalf("default audio rate = %d, want 48000", f.AudioSampleRate())
	}
}

func TestFMDemodulator_Name(t *testing.T) {
	if got := NewFMDemodulator(25000, 48000).Name(); got != "WFM" {
		t.Errorf("Name(25000) = %q, want WFM", got)
	}
	if got := NewFMDemodulator(10000, 48000).Name(); got != "WFM" {
		t.Errorf("Name(10000) = %q, want WFM (boundary)", got)
	}
	if got := NewFMDemodulator(2500, 48000).Name(); got != "NFM" {
		t.Errorf("Name(2500) = %q, want NFM", got)
	}
}

func TestFMDemodulator_CanHandle(t *testing.T) {
	f := NewFMDemodulator(25000, 48000)
	for _, mod := range []string{"FM", "WFM", "NFM", "fm", "wfm", "nfm"} {
		if !f.CanHandle(mod) {
			t.Errorf("CanHandle(%q) = false, want true", mod)
		}
	}
	for _, mod := range []string{"AM", "SSB", "USB", "", "CW"} {
		if f.CanHandle(mod) {
			t.Errorf("CanHandle(%q) = true, want false", mod)
		}
	}
}

func TestFMDemodulate_Errors(t *testing.T) {
	f := NewFMDemodulator(25000, 48000)
	if _, err := f.Demodulate([]complex64{1, 2, 3}, 48000); err == nil {
		t.Fatal("expected error for too few samples, got nil")
	}
	if _, err := f.Demodulate(make([]complex64, 16), 0); err == nil {
		t.Fatal("expected error for zero sample rate, got nil")
	}
}

func TestFMDemodulate_ConstantSignal(t *testing.T) {
	f := NewFMDemodulator(25000, 48000)
	iq := make([]complex64, 1024)
	for i := range iq {
		iq[i] = complex(1, 0) // no phase change → no frequency offset
	}
	out, err := f.Demodulate(iq, 48000)
	if err != nil {
		t.Fatalf("Demodulate: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
	for i, v := range out {
		if v != 0 {
			t.Fatalf("sample %d = %v, want 0 for constant signal", i, v)
		}
	}
}

func TestFMDemodulate_Tone(t *testing.T) {
	// Constant frequency offset of 1000 Hz on a 25 kHz deviation grid
	// should demodulate to a flat 1000/25000 = 0.04 audio level.
	const (
		sr   = 48000
		f0   = 1000.0
		dev  = 25000.0
		n    = 4800
		want = f0 / dev
	)
	iq := make([]complex64, n)
	for i := range iq {
		phase := 2 * math.Pi * f0 * float64(i) / sr
		iq[i] = complex64(complex(math.Cos(phase), math.Sin(phase)))
	}

	f := NewFMDemodulator(dev, 48000)
	out, err := f.Demodulate(iq, sr)
	if err != nil {
		t.Fatalf("Demodulate: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
	const tol = 0.005
	if math.Abs(float64(out[0])-want) > tol {
		t.Errorf("out[0] = %v, want ~%v", out[0], want)
	}
	if math.Abs(float64(out[len(out)/2])-want) > tol {
		t.Errorf("out[mid] = %v, want ~%v", out[len(out)/2], want)
	}
}

func TestRegistry_Get_Empty(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("WFM"); err == nil {
		t.Fatal("expected error from empty registry, got nil")
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(NewFMDemodulator(25000, 48000))
	d, err := r.Get("WFM")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Name() != "WFM" {
		t.Fatalf("Get(\"WFM\").Name() = %q, want WFM", d.Name())
	}
	if _, err := r.Get("AM"); err == nil {
		t.Fatal("expected error for unregistered modulation, got nil")
	}
	if _, err := r.Get("bogus"); err == nil {
		t.Fatal("expected error for unknown modulation, got nil")
	}
}

func TestRegistry_AllReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Register(NewFMDemodulator(25000, 48000))
	all := r.All()
	all[0] = nil
	if got := r.All(); len(got) != 1 || got[0] == nil {
		t.Fatalf("registry mutated through All() copy: %+v", got)
	}
}

func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()
	if got := len(r.All()); got != 5 {
		t.Fatalf("DefaultRegistry has %d demodulators, want 5", got)
	}
	d, err := r.Get("wfm")
	if err != nil {
		t.Fatalf("Get(\"wfm\"): %v", err)
	}
	if d.Name() != "WFM" {
		t.Fatalf("Get(\"wfm\").Name() = %q, want WFM", d.Name())
	}
	for _, mod := range []string{"AM", "am", "USB", "LSB", "ssb"} {
		if _, err := r.Get(mod); err != nil {
			t.Errorf("DefaultRegistry has no demodulator for %q: %v", mod, err)
		}
	}
	if _, err := r.Get("CW"); err == nil {
		t.Fatal("expected error for CW in default registry")
	}
}

func TestNormalizeModulation(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"wfm", "WFM"},
		{"  usb ", "USB"},
		{"NFM", "NFM"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeModulation(c.in); got != c.want {
			t.Errorf("NormalizeModulation(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
