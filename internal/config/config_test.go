package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	content := "name: test-svc\nport: 8080\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out struct {
		Name string `yaml:"name"`
		Port int    `yaml:"port"`
	}
	if err := Load(path, &out); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Name != "test-svc" || out.Port != 8080 {
		t.Fatalf("loaded {Name:%q Port:%d}, want {test-svc 8080}", out.Name, out.Port)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	var out struct{}
	if err := Load(filepath.Join(t.TempDir(), "nope.yaml"), &out); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("a: b: c\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var out struct{}
	if err := Load(path, &out); err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("SIGNAL_TEST_ENV", "value")
	if got := GetEnv("SIGNAL_TEST_ENV", "fallback"); got != "value" {
		t.Fatalf("GetEnv = %q, want value", got)
	}
	if got := GetEnv("SIGNAL_TEST_UNSET", "fallback"); got != "fallback" {
		t.Fatalf("GetEnv = %q, want fallback", got)
	}
}

func TestGetEnvInt(t *testing.T) {
	t.Setenv("SIGNAL_TEST_INT", "8080")
	if got := GetEnvInt("SIGNAL_TEST_INT", 1); got != 8080 {
		t.Fatalf("GetEnvInt = %d, want 8080", got)
	}
	if got := GetEnvInt("SIGNAL_TEST_UNSET", 42); got != 42 {
		t.Fatalf("GetEnvInt unset = %d, want 42", got)
	}
	t.Setenv("SIGNAL_TEST_BAD", "abc")
	if got := GetEnvInt("SIGNAL_TEST_BAD", 7); got != 7 {
		t.Fatalf("GetEnvInt non-numeric = %d, want 7", got)
	}
}

func TestResolveInt(t *testing.T) {
	if got := ResolveInt(0, 0, 5, 7); got != 5 {
		t.Errorf("ResolveInt(0,0,5,7) = %d, want 5", got)
	}
	if got := ResolveInt(0, 0); got != 0 {
		t.Errorf("ResolveInt(0,0) = %d, want 0", got)
	}
	if got := ResolveInt(3); got != 3 {
		t.Errorf("ResolveInt(3) = %d, want 3", got)
	}
}

func TestResolveFloat(t *testing.T) {
	if got := ResolveFloat(0, -60, -30); got != -60 {
		t.Errorf("ResolveFloat(0,-60,-30) = %v, want -60", got)
	}
	if got := ResolveFloat(0, 0); got != 0 {
		t.Errorf("ResolveFloat(0,0) = %v, want 0", got)
	}
}

func TestResolveString(t *testing.T) {
	if got := ResolveString("", "b", "c"); got != "b" {
		t.Errorf("ResolveString(\"\",b,c) = %q, want b", got)
	}
	if got := ResolveString("", ""); got != "" {
		t.Errorf("ResolveString empty = %q, want empty", got)
	}
}

// Typed configs must round-trip the exact YAML files shipped in
// config/ (§16.1: YAML is the single source of truth).
func TestLoadIQIngestConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iq-ingest.yaml")
	content := `listen_port: 9000
buffer_size: 256
consumers:
  - name: signal-processor
    host: signal-processor
    port: 9010
stats:
  interval_s: 5
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var cfg IQIngestConfig
	if err := Load(path, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenPort != 9000 {
		t.Errorf("ListenPort = %d, want 9000", cfg.ListenPort)
	}
	if len(cfg.Consumers) != 1 {
		t.Fatalf("consumers = %d, want 1", len(cfg.Consumers))
	}
	if got := cfg.Consumers[0].Addr(); got != "signal-processor:9010" {
		t.Errorf("consumer addr = %q, want signal-processor:9010", got)
	}
	if cfg.Consumers[0].Name != "signal-processor" {
		t.Errorf("consumer name = %q, want signal-processor", cfg.Consumers[0].Name)
	}
}

func TestLoadSignalProcessorConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signal-processor.yaml")
	content := `listen_port: 9010
peak_detection:
  threshold_db: -60
  min_spacing_bins: 10
  max_peaks: 20
fft:
  size: 4096
  window: hann
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var cfg SignalProcessorConfig
	if err := Load(path, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenPort != 9010 || cfg.PeakDetection.ThresholdDB != -60 ||
		cfg.PeakDetection.MinSpacingBins != 10 || cfg.PeakDetection.MaxPeaks != 20 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.FFT.Size != 4096 || cfg.FFT.Window != "hann" {
		t.Errorf("fft = %+v, want size 4096 window hann", cfg.FFT)
	}
}

func TestLoadClassifierConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "classifier.yaml")
	content := `model_path: models/classifier.onnx
rules:
  enabled: true
onnx:
  enabled: true
  min_confidence: 0.5
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var cfg ClassifierConfig
	if err := Load(path, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ModelPath != "models/classifier.onnx" {
		t.Errorf("ModelPath = %q, want models/classifier.onnx", cfg.ModelPath)
	}
	if !cfg.ONNX.Enabled || cfg.ONNX.MinConfidence != 0.5 {
		t.Errorf("onnx = %+v, want enabled with min_confidence 0.5", cfg.ONNX)
	}
}

func TestConsumerConfig_Addr(t *testing.T) {
	if got := (ConsumerConfig{Name: "x", Host: "h", Port: 1}).Addr(); got != "h:1" {
		t.Errorf("Addr = %q, want h:1", got)
	}
	if got := (ConsumerConfig{Name: "x", Host: "h"}).Addr(); got != "" {
		t.Errorf("Addr without port = %q, want empty", got)
	}
}
