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