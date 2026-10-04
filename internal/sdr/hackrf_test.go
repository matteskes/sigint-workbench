// Package sdr — HackRF driver tests that run in every build.
//
// The real driver (hackrf.go) is compile-validated with
// `go build -tags hackrf`; there is no HackRF in the test environment,
// so the tests here cover the pure parts and — critically — the H2
// guard: no HackRF source may reference the libhackrf transmit API.
package sdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitHackRFGain(t *testing.T) {
	// §15.4: one dB figure -> VGA first (0-62, 2 dB steps), then LNA
	// (0-40, 8 dB steps); monotonic, capped at 102 dB total.
	tests := []struct {
		db       float64
		lna, vga uint32
	}{
		{-5, 0, 0}, // negative maps to zero (SetGain rejects it)
		{0, 0, 0},
		{1, 0, 2},
		{30, 0, 30},
		{40, 0, 40},
		{50, 0, 50},
		{62, 0, 62},   // VGA exhausted
		{64, 0, 62},   // remainder 2 dB rounds to 0 LNA
		{66, 8, 62},   // remainder 4 dB rounds to 8 LNA
		{100, 40, 62}, // remainder 38 dB rounds to 40 LNA
		{110, 40, 62}, // clamped at the 102 dB ceiling
	}
	for _, tc := range tests {
		lna, vga := splitHackRFGain(tc.db)
		if lna != tc.lna || vga != tc.vga {
			t.Errorf("splitHackRFGain(%v) = (lna %d, vga %d), want (%d, %d)",
				tc.db, lna, vga, tc.lna, tc.vga)
		}
		if lna%hackRFLNAStepDB != 0 || vga%hackRFVGAStepDB != 0 {
			t.Errorf("splitHackRFGain(%v) = (%d, %d) off the %d/%d dB steps",
				tc.db, lna, vga, hackRFLNAStepDB, hackRFVGAStepDB)
		}
	}
}

func TestSplitHackRFGainMonotonic(t *testing.T) {
	var prevTotal int
	for db := 0.0; db <= 110; db += 0.5 {
		lna, vga := splitHackRFGain(db)
		total := int(lna) + int(vga)
		if total < prevTotal {
			t.Fatalf("gain mapping not monotonic at %v dB: %d < %d",
				db, total, prevTotal)
		}
		if total > 102 {
			t.Fatalf("gain mapping exceeded the 102 dB ceiling at %v dB: %d",
				db, total)
		}
		prevTotal = total
	}
}

// TestHackRFH2Guard is the hard constraint (H2, SPEC §1.2, §15.4): the
// workbench is receive-only. No HackRF driver source may reference the
// libhackrf transmit entry points, and the real driver must report
// HasTX=false.
func TestHackRFH2Guard(t *testing.T) {
	forbidden := []string{
		"hackrf_start_tx",
		"hackrf_stop_tx",
		"hackrf_enable_tx",  // flush/block-complete TX plumbing
		"hackrf_init_sweep", // TX-oriented sweep mode
		"HackRFTX",          // no TX type may creep in
	}
	for _, name := range []string{"hackrf.go", "hackrf_stub.go", "hackrf_gain.go"} {
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// Strip // comments so doc mentions of the forbidden symbols
		// (hackrf.go's header states the H2 policy by name) do not
		// trip the guard; only actual code references count.
		var code strings.Builder
		for _, line := range strings.Split(string(data), "\n") {
			if i := strings.Index(line, "//"); i >= 0 {
				line = line[:i]
			}
			code.WriteString(line)
			code.WriteString("\n")
		}
		src := code.String()
		for _, sym := range forbidden {
			if strings.Contains(src, sym) {
				t.Errorf("H2 violation: %s references %q (HackRF is RX-only)",
					name, sym)
			}
		}
	}
	// The real driver pins HasTX=false in its metadata; assert via the
	// source so the check also runs in untagged builds.
	data, err := os.ReadFile(filepath.Join(".", "hackrf.go"))
	if err != nil {
		t.Fatalf("read hackrf.go: %v", err)
	}
	sawHasTX := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "HasTX:") {
			sawHasTX = true
			if !strings.Contains(line, "false") {
				t.Errorf("H2 violation: hackrf.go HasTX is not false: %q", line)
			}
		}
	}
	if !sawHasTX {
		t.Error("H2 violation: hackrf.go never sets HasTX")
	}
}

func TestNewHackRFStubError(t *testing.T) {
	// In builds without the hackrf tag the constructor must fail with
	// a clear remediation hint (mirrors the rtlsdr stub contract).
	_, err := NewHackRF("hackrf-0", "0000000000000000")
	if err == nil {
		t.Fatal("expected error from stub constructor, got nil")
	}
	if !strings.Contains(err.Error(), "-tags hackrf") {
		t.Errorf("error %q does not mention -tags hackrf", err)
	}
}
