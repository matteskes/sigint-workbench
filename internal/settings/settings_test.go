package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"sigint-workbench/internal/config"
	"sigint-workbench/internal/sdr"
)

const captureYAML = `# sdr-capture configuration (§7)
# ─── Devices ───
sdrs:
  - id: rtlsdr-0
    driver: rtlsdr
    usb_index: 0
    default_freq: 162550000
    default_gain: 32.8
    default_bw: 2400000
    mode: scanner
    stream_host: iq-ingest
    stream_port: 9000
    # §6.2 calibration offset from bin/rtl-calibrate
    calibration_offset_db: 1.4
    lat: 37.7749
    lon: -122.4194
  - id: rtlsdr-1
    driver: rtlsdr
    default_freq: 162550000
    default_gain: 32.8
    default_bw: 2400000
    mode: monitor
    stream_host: iq-ingest
    stream_port: 9000
# ─── Scan loop (D3) ───
scan:
  step_hz: 100000
  dwell_ms: 50
`

const processingYAML = `# signal-processor configuration (§5)
listen_port: 9100
peak_detection:
  threshold_db: -60
  min_spacing_bins: 3
  max_peaks: 8
fft:
  size: 4096
  window: rectangular
scan:
  step_hz: 100000
  dwell_ms: 50
  min_freq_hz: 87500000
  max_freq_hz: 108000000
spectrum:
  bins: 256
  rate_hz: 5
`

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotCapture(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "sdr-capture.yaml", captureYAML)
	st := NewStore(dir)
	vals := st.Snapshot()["capture"]
	if vals == nil {
		t.Fatal("capture snapshot missing")
	}
	if got := vals["scan.step_hz"]; got != 100000.0 {
		t.Fatalf("scan.step_hz = %v, want 100000", got)
	}
	if got := vals["scan.dwell_ms"]; got != 50.0 {
		t.Fatalf("scan.dwell_ms = %v, want 50", got)
	}
	if _, ok := vals["stream_format"]; ok {
		t.Fatal("stream_format should be absent so the UI default applies")
	}
	devices, ok := vals["sdrs"].([]map[string]any)
	if !ok || len(devices) != 2 {
		t.Fatalf("sdrs = %#v, want 2 devices", vals["sdrs"])
	}
	if _, ok := devices[0]["calibration_offset_db"]; !ok {
		t.Fatal("device 0 should expose calibration_offset_db")
	}
	if _, ok := devices[1]["calibration_offset_db"]; ok {
		t.Fatal("device 1 has no offset in the file; presence must stay absent")
	}
	if got := devices[1]["stream_host"]; got != "iq-ingest" {
		t.Fatalf("stream_host = %v", got)
	}
}

// A scan block that is entirely absent still snapshots with the
// documented §7.1 defaults, mirroring what the service resolves.
func TestSnapshotScanDefaults(t *testing.T) {
	dir := t.TempDir()
	minimal := "sdrs:\n  - id: sim-0\n    driver: simulator\n    default_freq: 162550000\n    default_gain: 20\n    default_bw: 2400000\n    mode: monitor\n"
	write(t, dir, "sdr-capture.yaml", minimal)
	vals := NewStore(dir).Snapshot()["capture"]
	if vals == nil {
		t.Fatal("snapshot missing")
	}
	if got := vals["scan.step_hz"]; got != 100000.0 {
		t.Fatalf("default step = %v, want 100000", got)
	}
	if got := vals["scan.dwell_ms"]; got != 50.0 {
		t.Fatalf("default dwell = %v, want 50", got)
	}
	devices := vals["sdrs"].([]map[string]any)
	if got := devices[0]["stream_host"]; got != "localhost" {
		t.Fatalf("implicit host should display as localhost, got %v", got)
	}
}

// Saving the sdrs list rewrites the list nodes; comments on scalars
// inside list items are not preserved, but the top-level structure
// comments and every surviving value are.
func TestSaveRewritesListPreservingComments(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "sdr-capture.yaml", captureYAML)
	st := NewStore(dir)
	devices := []map[string]any{
		{
			"id": "rtlsdr-0", "driver": "rtlsdr", "usb_index": 0.0,
			"default_freq": 162550000.0, "default_gain": 40.0,
			"default_bw": 2400000.0, "mode": "scanner",
			"stream_host": "iq-ingest", "stream_port": 9000.0,
			"calibration_offset_db": 1.4, "lat": 37.7749, "lon": -122.4194,
		},
		{
			"id": "rtlsdr-1", "driver": "rtlsdr", "default_freq": 162550000.0,
			"default_gain": 32.8, "default_bw": 2400000.0, "mode": "monitor",
			"stream_host": "iq-ingest", "stream_port": 9001.0,
		},
		{
			"id": "rtlsdr-2", "driver": "rtlsdr", "usb_index": 1.0,
			"default_freq": 162550000.0, "default_gain": 20.0,
			"default_bw": 2400000.0, "mode": "both",
			"stream_host": "iq-ingest", "stream_port": 9002.0,
		},
	}
	res, err := st.Save("capture", map[string]any{"sdrs": devices})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if res.File != "sdr-capture.yaml" || len(res.Restart) != 2 {
		t.Fatalf("result = %#v", res)
	}
	out, err := os.ReadFile(filepath.Join(dir, "sdr-capture.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "# ─── Scan loop (D3) ───") {
		t.Fatal("scan section comment was dropped")
	}
	cfg, err := sdr.LoadCaptureConfig(filepath.Join(dir, "sdr-capture.yaml"))
	if err != nil {
		t.Fatalf("service reload: %v", err)
	}
	if len(cfg.SDRs) != 3 || cfg.SDRs[0].DefaultGain != 40 || cfg.SDRs[2].ID != "rtlsdr-2" {
		t.Fatalf("reloaded devices = %#v", cfg.SDRs)
	}
	if cfg.SDRs[0].CalibrationOffsetDB == nil || *cfg.SDRs[0].CalibrationOffsetDB != 1.4 {
		t.Fatal("calibration offset lost in rewrite")
	}
	if cfg.SDRs[1].CalibrationOffsetDB != nil {
		t.Fatal("device 1 must stay uncalibrated")
	}
}

// Saving a block that is absent from the file creates it, marks it
// with the setup comment, and the typed round-trip sees it.
func TestSaveCreatesMissingBlock(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "signal-processor.yaml", processingYAML)
	st := NewStore(dir)
	_, err := st.Save("processing", map[string]any{
		"tdoa.enabled":   true,
		"tdoa.window_ms": 20.0,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "signal-processor.yaml"))
	text := string(out)
	if !strings.Contains(text, "tdoa:") || !strings.Contains(text, "window_ms: 20") {
		t.Fatalf("tdoa block missing from output:\n%s", text)
	}
	if !strings.Contains(text, "# Configured via the setup screen (SPEC §20).") {
		t.Fatal("created keys should carry the setup marker comment")
	}
	var cfg config.SignalProcessorConfig
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.TDOA.Enabled || cfg.TDOA.WindowMS != 20 {
		t.Fatalf("tdoa round-trip = %#v\nFILE:\n%s", cfg.TDOA, text)
	}
}

func TestSaveValidationLeavesFileUntouched(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "signal-processor.yaml", processingYAML)
	st := NewStore(dir)
	before, _ := os.ReadFile(filepath.Join(dir, "signal-processor.yaml"))
	_, err := st.Save("processing", map[string]any{"spectrum.bins": 300.0})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "spectrum.bins" {
		t.Fatalf("err = %v, want ValidationError(spectrum.bins)", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "signal-processor.yaml"))
	if string(before) != string(after) {
		t.Fatal("rejected save must not modify the file")
	}
	if _, err := os.Stat(filepath.Join(dir, "signal-processor.yaml.bak")); err == nil {
		t.Fatal("rejected save must not leave a backup")
	}
	_, err = st.Save("processing", map[string]any{"nope.key": 1.0})
	if err == nil || !strings.Contains(err.Error(), "unknown setting") {
		t.Fatalf("unknown key err = %v", err)
	}
}

func TestSaveMissingRequiredListItemField(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "sdr-capture.yaml", captureYAML)
	st := NewStore(dir)
	_, err := st.Save("capture", map[string]any{"sdrs": []map[string]any{
		{"driver": "rtlsdr", "default_freq": 162550000.0},
	}})
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Field, "sdrs[0].id") ||
		!strings.Contains(ve.Error(), "required") {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveWritesBackup(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "sdr-capture.yaml", captureYAML)
	st := NewStore(dir)
	if _, err := st.Save("capture", map[string]any{"scan.step_hz": 200000.0}); err != nil {
		t.Fatalf("save: %v", err)
	}
	bak, err := os.ReadFile(filepath.Join(dir, "sdr-capture.yaml.bak"))
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if string(bak) != captureYAML {
		t.Fatal("backup should hold the pre-save content")
	}
	out, _ := os.ReadFile(filepath.Join(dir, "sdr-capture.yaml"))
	var cfg sdr.CaptureConfig
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Scan.StepHz != 200000 {
		t.Fatalf("step_hz = %d", cfg.Scan.StepHz)
	}
}

// Optional strings model presence: empty removes the key again.
func TestSaveOptionalStringRemoval(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "sdr-capture.yaml", captureYAML)
	st := NewStore(dir)
	if _, err := st.Save("capture", map[string]any{"stream_format": "sdr2"}); err != nil {
		t.Fatalf("save sdr2: %v", err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "sdr-capture.yaml"))
	if !strings.Contains(string(out), "stream_format: sdr2") {
		t.Fatal("stream_format should be set")
	}
	if _, err := st.Save("capture", map[string]any{"stream_format": ""}); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	out, _ = os.ReadFile(filepath.Join(dir, "sdr-capture.yaml"))
	if strings.Contains(string(out), "stream_format") {
		t.Fatal("empty optional string should remove the key")
	}
}

// Drift guard: the schema must keep covering the shipped repo configs.
func TestSchemaSnapshotCoversRepoConfigs(t *testing.T) {
	st := NewStore(filepath.Join("..", "..", "config"))
	snap := st.Snapshot()
	for _, sec := range Schema() {
		if snap[sec.ID] == nil {
			t.Fatalf("section %s (%s) failed to snapshot against the repo config", sec.ID, sec.File)
		}
	}
	p := snap["processing"]
	if got := p["fft.size"]; got != 4096.0 {
		t.Fatalf("fft.size = %v, want 4096", got)
	}
	if got := p["spectrum.bins"]; got != 256.0 {
		t.Fatalf("spectrum.bins = %v, want 256", got)
	}
	if got := p["tdoa.window_ms"]; got != 10.0 {
		t.Fatalf("tdoa.window_ms = %v, want 10", got)
	}
	r := snap["recorder"]
	if got := r["audio.sample_rate"]; got != 48000.0 {
		t.Fatalf("audio.sample_rate = %v, want 48000", got)
	}
	if got := r["stream.bitrate_bps"]; got != 24000.0 {
		t.Fatalf("stream.bitrate_bps = %v, want 24000", got)
	}
	if got := r["tfr.max_nfft"]; got != 16384.0 {
		t.Fatalf("tfr.max_nfft = %v, want 16384", got)
	}
	i := snap["ingest"]
	if got := i["stats.interval_s"]; got != 5.0 {
		t.Fatalf("stats.interval_s = %v, want 5", got)
	}
	c := snap["classifier"]
	if got := c["onnx.min_confidence"]; got != 0.5 {
		t.Fatalf("onnx.min_confidence = %v, want 0.5", got)
	}
	devices := snap["capture"]["sdrs"].([]map[string]any)
	if len(devices) == 0 || devices[0]["id"] == "" {
		t.Fatal("capture snapshot should list the shipped devices")
	}
}

func TestCoerceScalarBasics(t *testing.T) {
	f := Field{Key: "x", Type: "number", Integer: true}
	v, err := coerceScalar(f, 4096.0)
	if err != nil || v.(int64) != 4096 {
		t.Fatalf("v=%v err=%v", v, err)
	}
	if _, err := coerceScalar(f, 4096.5); err == nil {
		t.Fatal("fractional value must be rejected for integer fields")
	}
	if _, err := coerceScalar(f, true); err == nil {
		t.Fatal("bool must be rejected on a number field")
	}
	sel := Field{Key: "w", Type: "select", Options: []string{"hann", "hamming"}}
	if _, err := coerceScalar(sel, "kaiser"); err == nil {
		t.Fatal("invalid option must be rejected")
	}
	if v, err := coerceScalar(sel, "hann"); err != nil || v.(string) != "hann" {
		t.Fatalf("v=%v err=%v", v, err)
	}
}

func TestStoreGuards(t *testing.T) {
	if err := NewStore(filepath.Join(t.TempDir(), "missing")).Available(); err == nil {
		t.Fatal("Available must fail for a missing directory")
	}
	if _, err := NewStore(t.TempDir()).Save("bogus", nil); !errors.Is(err, ErrUnknownSection) {
		t.Fatalf("err = %v, want ErrUnknownSection", err)
	}
}
