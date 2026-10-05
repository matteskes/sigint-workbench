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

func TestLoadCaptureConfig_CalibrationAndAPIPort(t *testing.T) {
	// §5.6: calibration_offset_db is optional per SDR; PRESENCE of the
	// key marks the device calibrated, absence leaves it nil. api_port
	// is the capture control-API port consumed by signal-processor's
	// gain polling (0 = off).
	cfg, err := LoadCaptureConfig(writeCaptureConfig(t, `
api_port: 9090
sdrs:
  - id: "rtlsdr-0"
    driver: rtlsdr
    default_freq: 146520000
    default_gain: 40
    calibration_offset_db: -12.5
  - id: "simulator-0"
    driver: simulator
    default_freq: 100000000
    default_gain: 40
    calibration_offset_db: 0.0
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIPort != 9090 {
		t.Errorf("api_port = %d, want 9090", cfg.APIPort)
	}
	if got := cfg.SDRs[0].CalibrationOffsetDB; got == nil || *got != -12.5 {
		t.Errorf("rtlsdr-0 calibration_offset_db = %v, want -12.5", got)
	}
	if got := cfg.SDRs[1].CalibrationOffsetDB; got == nil || *got != 0.0 {
		t.Errorf("simulator-0 calibration_offset_db = %v, want 0.0 (explicit)", got)
	}
	// Absent key → nil (uncalibrated).
	cfg2, err := LoadCaptureConfig(writeCaptureConfig(t, scannerSDR))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg2.SDRs[0].CalibrationOffsetDB != nil {
		t.Errorf("calibration_offset_db = %v, want nil when absent",
			cfg2.SDRs[0].CalibrationOffsetDB)
	}
	if cfg2.APIPort != 0 {
		t.Errorf("api_port = %d, want 0 (polling disabled) when absent", cfg2.APIPort)
	}
}

func TestLoadCaptureConfig_SimulatorDriver(t *testing.T) {
	// §16.2 documents driver: simulator — the loader must accept it so
	// hardware-less multi-SDR development works. This fixture is the
	// config-rehearsal shape: two sim devices sharing one iq-ingest UDP
	// port (frames carry sdr_id, §16.4), same center so the §8
	// verification pair forms between them.
	cfg, err := LoadCaptureConfig(writeCaptureConfig(t, `
sdrs:
  - id: "simulator-0"
    driver: simulator
    default_freq: 100000000
    default_gain: 40
    default_bw: 2400000
    mode: monitor
    stream_port: 9000
    lat: 40.7128
    lon: -74.0060
  - id: "simulator-1"
    driver: simulator
    default_freq: 100000000
    default_gain: 40
    default_bw: 2400000
    mode: monitor
    stream_port: 9000
    lat: 40.7128
    lon: -73.9950
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i, want := range []string{"simulator-0", "simulator-1"} {
		if cfg.SDRs[i].Driver != "simulator" {
			t.Errorf("sdr[%d] driver = %q, want simulator", i, cfg.SDRs[i].Driver)
		}
		if cfg.SDRs[i].ID != want {
			t.Errorf("sdr[%d] id = %q, want %q", i, cfg.SDRs[i].ID, want)
		}
		if cfg.SDRs[i].StreamHost != "localhost" || cfg.SDRs[i].StreamPort != 9000 {
			t.Errorf("sdr[%d] stream = %s:%d, want localhost:9000 (shared port)",
				i, cfg.SDRs[i].StreamHost, cfg.SDRs[i].StreamPort)
		}
	}
	// Unknown drivers are still rejected.
	bad := `
sdrs:
  - id: "x"
    driver: plutosdr
    default_freq: 100000000
`
	if _, err := LoadCaptureConfig(writeCaptureConfig(t, bad)); err == nil {
		t.Error("expected error for unknown driver, got nil")
	}
}

// §4.5/§9.6: stream_format selects the wire format, defaulting to
// sdr1; only sdr1|sdr2 are accepted.
func TestLoadCaptureConfig_StreamFormat(t *testing.T) {
	// Absent → sdr1 default.
	cfg, err := LoadCaptureConfig(writeCaptureConfig(t, scannerSDR))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.WireFormat(); got != "sdr1" {
		t.Errorf("WireFormat = %q, want default %q", got, "sdr1")
	}
	if cfg.StreamFormat != "sdr1" {
		t.Errorf("StreamFormat = %q, want normalized %q", cfg.StreamFormat, "sdr1")
	}

	// Explicit sdr2 resolves through.
	cfg2, err := LoadCaptureConfig(writeCaptureConfig(t, `
stream_format: sdr2
`+scannerSDR))
	if err != nil {
		t.Fatalf("load sdr2: %v", err)
	}
	if got := cfg2.WireFormat(); got != "sdr2" {
		t.Errorf("WireFormat = %q, want %q", got, "sdr2")
	}

	// Unknown values are rejected.
	bad := "stream_format: sdr3\n" + scannerSDR
	if _, err := LoadCaptureConfig(writeCaptureConfig(t, bad)); err == nil {
		t.Error("expected error for unknown stream_format, got nil")
	}
}

