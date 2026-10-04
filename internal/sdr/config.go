// Package sdr — configuration for the sdr-capture service.
package sdr

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Scan loop defaults (§7.1): 100 kHz resolution, 50 ms dwell.
const (
	DefaultScanStepHz  uint64 = 100_000
	DefaultScanDwellMS int    = 50
)

// CaptureConfig is the top-level configuration for sdr-capture.
type CaptureConfig struct {
	SDRs []SDRCaptureConfig `yaml:"sdrs"`
	Scan ScanConfig         `yaml:"scan,omitempty"`
}

// ScanConfig configures the D3 scan loop (§7.1) applied to every device
// whose mode is "scanner" or "both". Zero-valued fields fall back to the
// documented defaults (100 kHz step, 50 ms dwell); a zero frequency range
// defaults to — and is clamped to — the driver's capability range
// (RTL-SDR: 24–1766 MHz), resolved per device at startup.
type ScanConfig struct {
	StepHz  uint64 `yaml:"step_hz"`  // sweep step in Hz (default 100 kHz)
	DwellMS int    `yaml:"dwell_ms"` // dwell per frequency in ms (default 50)
	MinHz   uint64 `yaml:"min_hz"`   // sweep start (default: driver minimum)
	MaxHz   uint64 `yaml:"max_hz"`   // sweep end (default: driver maximum)
}

// Step returns the configured sweep step or the 100 kHz default.
func (c ScanConfig) Step() uint64 {
	if c.StepHz == 0 {
		return DefaultScanStepHz
	}
	return c.StepHz
}

// Dwell returns the configured dwell or the 50 ms default.
func (c ScanConfig) Dwell() time.Duration {
	if c.DwellMS <= 0 {
		return time.Duration(DefaultScanDwellMS) * time.Millisecond
	}
	return time.Duration(c.DwellMS) * time.Millisecond
}

// SDRCaptureConfig configures a single SDR device.
type SDRCaptureConfig struct {
	ID          string  `yaml:"id"`                  // Logical ID, e.g. "rtlsdr-0"
	Driver      string  `yaml:"driver"`              // "rtlsdr" or "hackrf"
	USBIndex    int     `yaml:"usb_index,omitempty"` // For RTL-SDR
	Serial      string  `yaml:"serial,omitempty"`    // For HackRF
	DefaultFreq uint64  `yaml:"default_freq"`        // Hz
	DefaultGain float64 `yaml:"default_gain"`        // dB
	DefaultBW   uint32  `yaml:"default_bw"`          // Hz
	Mode        string  `yaml:"mode"`                // "scanner" | "monitor" | "both"

	// Streaming target
	StreamHost string `yaml:"stream_host"` // e.g. "localhost" or "iq-ingest"
	StreamPort int    `yaml:"stream_port"` // e.g. 9000

	// Location (optional) — physical location of the receiver, used
	// for map placement of signals that cannot be located otherwise.
	Lat *float64 `yaml:"lat,omitempty"`
	Lon *float64 `yaml:"lon,omitempty"`
}

// LoadCaptureConfig reads and parses a YAML config file.
func LoadCaptureConfig(path string) (*CaptureConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sdr: read config: %w", err)
	}
	var cfg CaptureConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("sdr: parse config: %w", err)
	}
	if len(cfg.SDRs) == 0 {
		return nil, fmt.Errorf("sdr: no SDRs configured")
	}
	// Validate
	for i, s := range cfg.SDRs {
		if s.ID == "" {
			return nil, fmt.Errorf("sdr[%d]: id is required", i)
		}
		switch s.Driver {
		case "rtlsdr", "hackrf", "simulator":
		default:
			return nil, fmt.Errorf("sdr[%d]: unknown driver %q", i, s.Driver)
		}
		if s.DefaultFreq == 0 {
			return nil, fmt.Errorf("sdr[%d]: default_freq is required", i)
		}
		if s.StreamHost == "" {
			s.StreamHost = "localhost"
			cfg.SDRs[i].StreamHost = s.StreamHost
		}
		if s.StreamPort == 0 {
			cfg.SDRs[i].StreamPort = 9000 + i
		}
		switch s.Mode {
		case "":
			s.Mode = "monitor"
			cfg.SDRs[i].Mode = s.Mode
		case "scanner", "monitor", "both":
		default:
			return nil, fmt.Errorf("sdr[%d]: unknown mode %q (want scanner|monitor|both)", i, s.Mode)
		}
		if (s.Lat == nil) != (s.Lon == nil) {
			return nil, fmt.Errorf("sdr[%d]: lat and lon must be set together", i)
		}
	}
	if cfg.Scan.MinHz != 0 && cfg.Scan.MaxHz != 0 && cfg.Scan.MinHz >= cfg.Scan.MaxHz {
		return nil, fmt.Errorf("sdr: scan.min_hz (%d) must be below scan.max_hz (%d)",
			cfg.Scan.MinHz, cfg.Scan.MaxHz)
	}
	return &cfg, nil
}
