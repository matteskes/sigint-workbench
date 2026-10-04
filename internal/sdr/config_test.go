package sdr

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCaptureConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capture.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

const scannerSDR = `
sdrs:
  - id: "rtlsdr-0"
    driver: rtlsdr
    default_freq: 146520000
    default_gain: 40
    default_bw: 2400000
    mode: scanner
`

func TestLoadCaptureConfig_ScanDefaults(t *testing.T) {
	cfg, err := LoadCaptureConfig(writeCaptureConfig(t, scannerSDR))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Scan.Step(); got != DefaultScanStepHz {
		t.Errorf("Step = %d, want %d", got, DefaultScanStepHz)
	}
	if got := cfg.Scan.Dwell(); got != time.Duration(DefaultScanDwellMS)*time.Millisecond {
		t.Errorf("Dwell = %v, want %v", got, time.Duration(DefaultScanDwellMS)*time.Millisecond)
	}
	if cfg.Scan.MinHz != 0 || cfg.Scan.MaxHz != 0 {
		t.Errorf("range = %d-%d, want unset (driver capability default)", cfg.Scan.MinHz, cfg.Scan.MaxHz)
	}
}

func TestLoadCaptureConfig_ScanOverrides(t *testing.T) {
	cfg, err := LoadCaptureConfig(writeCaptureConfig(t, scannerSDR+`
scan:
  step_hz: 25000
  dwell_ms: 10
  min_hz: 100000000
  max_hz: 200000000
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Scan.Step() != 25_000 || cfg.Scan.Dwell() != 10*time.Millisecond {
		t.Errorf("step/dwell = %d/%v, want 25000/10ms", cfg.Scan.Step(), cfg.Scan.Dwell())
	}
	if cfg.Scan.MinHz != 100_000_000 || cfg.Scan.MaxHz != 200_000_000 {
		t.Errorf("range = %d-%d", cfg.Scan.MinHz, cfg.Scan.MaxHz)
	}
}

func TestLoadCaptureConfig_ModeDefaultAndValidation(t *testing.T) {
	// Mode omitted defaults to monitor.
	cfg, err := LoadCaptureConfig(writeCaptureConfig(t, `
sdrs:
  - id: "x"
    driver: rtlsdr
    default_freq: 100000000
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SDRs[0].Mode != "monitor" {
		t.Errorf("mode = %q, want monitor", cfg.SDRs[0].Mode)
	}
	// Unknown mode is rejected.
	bad := `
sdrs:
  - id: "x"
    driver: rtlsdr
    default_freq: 100000000
    mode: bogus
`
	if _, err := LoadCaptureConfig(writeCaptureConfig(t, bad)); err == nil {
		t.Error("expected error for unknown mode, got nil")
	}
}

func TestLoadCaptureConfig_ScanRangeValidation(t *testing.T) {
	if _, err := LoadCaptureConfig(writeCaptureConfig(t, scannerSDR+`
scan:
  min_hz: 200000000
  max_hz: 100000000
`)); err == nil {
		t.Error("expected error for inverted scan range, got nil")
	}
}
