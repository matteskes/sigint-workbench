package main

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"sigint-workbench/internal/sdr"
)

// fakeSDR records SetFrequency calls so tests can observe the sweep.
type fakeSDR struct {
	mu     sync.Mutex
	freqs  []uint64
	failOn uint64 // return an error when tuning this frequency
}

func (f *fakeSDR) Open() error  { return nil }
func (f *fakeSDR) Close() error { return nil }

func (f *fakeSDR) SetFrequency(hz uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.freqs = append(f.freqs, hz)
	if f.failOn != 0 && hz == f.failOn {
		return fmt.Errorf("tune %d failed", hz)
	}
	return nil
}

func (f *fakeSDR) SetSampleRate(hz uint32) error   { return nil }
func (f *fakeSDR) SetGain(db float64) error        { return nil }
func (f *fakeSDR) ReadIQ(buf []int16) (int, error) { return len(buf), nil }
func (f *fakeSDR) Metadata() sdr.SDRMetadata {
	return sdr.SDRMetadata{ID: "fake-0", Model: "FakeRTL", FreqMin: 24_000_000, FreqMax: 1_700_000_000}
}

func (f *fakeSDR) tuned() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.freqs...)
}

// compile-time check that fakeSDR satisfies the driver interface.
var _ sdr.SDR = (*fakeSDR)(nil)

func newTestSlot(dev sdr.SDR) *sdrSlot {
	return &sdrSlot{
		cfg:       sdr.SDRCaptureConfig{ID: "fake-0", Driver: "simulator", DefaultBW: 2_400_000},
		device:    dev,
		scanning:  true,
		scanStep:  100_000,
		scanDwell: 2 * time.Millisecond,
		scanMinHz: 100_000_000,
		scanMaxHz: 100_300_000,
		freqHz:    100_000_000,
	}
}

func TestNextScanFreq(t *testing.T) {
	// §7.1: f = min + k·step, wrapping to min past max.
	const min, max, step = 100_000_000, 100_300_000, 100_000
	f := uint64(min)
	for i := 1; i <= 3; i++ {
		next, ok := nextScanFreq(f, step, min, max)
		if !ok {
			t.Fatal("unexpected degenerate range")
		}
		f = next
		if want := uint64(min + i*step); f != want {
			t.Fatalf("step %d: f = %d, want %d", i, f, want)
		}
	}
	// 100.4 MHz would exceed max → wrap to min.
	next, _ := nextScanFreq(f, step, min, max)
	if next != min {
		t.Fatalf("wrap: got %d, want %d", next, min)
	}
	// Degenerate ranges disable the sweep.
	if _, ok := nextScanFreq(min, 0, min, max); ok {
		t.Error("step 0 should disable the sweep")
	}
	if _, ok := nextScanFreq(min, step, max, max); ok {
		t.Error("min == max should disable the sweep")
	}
	if _, ok := nextScanFreq(min, step, max, min); ok {
		t.Error("min > max should disable the sweep")
	}
	// Out-of-range current frequency re-anchors at min.
	next, _ = nextScanFreq(50_000_000, step, min, max)
	if next != min {
		t.Errorf("re-anchor: got %d, want %d", next, min)
	}
}

func TestScanLoop_SweepsAndWraps(t *testing.T) {
	dev := &fakeSDR{}
	slot := newTestSlot(dev)
	exit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		scanLoop(slot, exit)
		close(done)
	}()

	time.Sleep(120 * time.Millisecond)
	close(exit)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scanLoop did not exit")
	}

	got := dev.tuned()
	if len(got) < 5 {
		t.Fatalf("expected multiple tunes, got %d (%v)", len(got), got)
	}
	// Deterministic walk: min + (k·step mod (span+step)) for k = i+1,
	// i.e. 100.1, 100.2, 100.3, 100.0, then repeating.
	span := slot.scanMaxHz - slot.scanMinHz
	sawWrap := false
	for i, f := range got {
		want := slot.scanMinHz + (uint64(i+1)*slot.scanStep)%(span+slot.scanStep)
		if f != want {
			t.Fatalf("tune %d: got %.0f kHz, want %.0f kHz (all: %v)",
				i, float64(f)/1e3, float64(want)/1e3, got)
		}
		if f == slot.scanMinHz {
			sawWrap = true
		}
	}
	if !sawWrap {
		t.Errorf("sweep never wrapped to min: %v", got)
	}
}

func TestScanLoop_ManualTunePauses(t *testing.T) {
	dev := &fakeSDR{}
	slot := newTestSlot(dev)
	exit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		scanLoop(slot, exit)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	if err := slot.setFrequency(146.52); err != nil {
		t.Fatalf("manual tune: %v", err)
	}
	slot.mu.Lock()
	paused := slot.scanPaused
	slot.mu.Unlock()
	if !paused {
		t.Fatal("manual tune did not pause the scan loop")
	}
	n := len(dev.tuned())
	time.Sleep(60 * time.Millisecond)
	after := dev.tuned()
	close(exit)
	<-done
	if len(after) != n {
		t.Errorf("scan loop kept tuning after pause: %d -> %d tunes", n, len(after))
	}
}

func TestScanLoop_TuneErrorContinues(t *testing.T) {
	dev := &fakeSDR{failOn: 100_200_000}
	slot := newTestSlot(dev)
	exit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		scanLoop(slot, exit)
		close(done)
	}()
	time.Sleep(80 * time.Millisecond)
	close(exit)
	<-done

	var sawFailed, sawAfter bool
	for _, f := range dev.tuned() {
		switch f {
		case 100_200_000:
			sawFailed = true
		case 100_300_000:
			sawAfter = true
		}
	}
	if !sawFailed {
		t.Error("sweep never attempted the failing frequency")
	}
	if !sawAfter {
		t.Error("sweep did not continue past the failing frequency")
	}
}

func TestResolveScanRange(t *testing.T) {
	meta := sdr.SDRMetadata{FreqMin: 24_000_000, FreqMax: 1_700_000_000}

	// Zero config defaults to the driver's capability range.
	min, max, ok := resolveScanRange(sdr.ScanConfig{}, meta)
	if !ok || min != meta.FreqMin || max != meta.FreqMax {
		t.Fatalf("defaults: %d-%d ok=%v, want %d-%d true", min, max, ok, meta.FreqMin, meta.FreqMax)
	}

	// Configured values are clamped into the driver range.
	min, max, ok = resolveScanRange(sdr.ScanConfig{MinHz: 1_000_000, MaxHz: 2_000_000_000}, meta)
	if !ok || min != meta.FreqMin || max != meta.FreqMax {
		t.Fatalf("clamp: %d-%d ok=%v, want %d-%d true", min, max, ok, meta.FreqMin, meta.FreqMax)
	}

	// Partial config: only max_hz set, min falls back to driver min.
	min, max, ok = resolveScanRange(sdr.ScanConfig{MaxHz: 200_000_000}, meta)
	if !ok || min != meta.FreqMin || max != 200_000_000 {
		t.Fatalf("partial: %d-%d ok=%v, want %d-200000000 true", min, max, ok, meta.FreqMin)
	}

	// Degenerate after clamping → scan disabled.
	if _, _, ok := resolveScanRange(sdr.ScanConfig{MinHz: 1_800_000_000, MaxHz: 2_000_000_000}, meta); ok {
		t.Error("range fully above driver capability should disable the sweep")
	}
}

func TestSlotStatusFields(t *testing.T) {
	dev := &fakeSDR{}
	slot := newTestSlot(dev)
	slot.scanning = true

	st := slot.status()
	for _, key := range []string{"id", "driver", "model", "active", "freq_hz", "freq_mhz",
		"gain_db", "bw_hz", "sample_rate", "mode", "scanning", "scan_paused", "stream"} {
		if _, ok := st[key]; !ok {
			t.Errorf("status missing %q", key)
		}
	}
	if st["active"] != false {
		t.Error("active should be false before any IQ read")
	}
	if st["scanning"] != true || st["scan_paused"] != false {
		t.Errorf("scanning = %v, scan_paused = %v, want true/false", st["scanning"], st["scan_paused"])
	}
	if st["freq_hz"] != uint64(100_000_000) {
		t.Errorf("freq_hz = %v, want %d", st["freq_hz"], 100_000_000)
	}
	if st["freq_mhz"] != 100.0 {
		t.Errorf("freq_mhz = %v, want 100.0", st["freq_mhz"])
	}

	// A fresh IQ read flips active (§7.4).
	slot.lastRead.Store(time.Now().UnixNano())
	if st = slot.status(); st["active"] != true {
		t.Error("active should be true right after a read")
	}
}
