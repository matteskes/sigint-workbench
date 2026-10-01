// Package sdr — configuration for the sdr-capture service.
package sdr

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// CaptureConfig is the top-level configuration for sdr-capture.
type CaptureConfig struct {
	SDRs []SDRCaptureConfig `yaml:"sdrs"`
}

// SDRCaptureConfig configures a single SDR device.
type SDRCaptureConfig struct {
	ID          string `yaml:"id"`                   // Logical ID, e.g. "rtlsdr-0"
	Driver      string `yaml:"driver"`               // "rtlsdr" or "hackrf"
	USBIndex    int    `yaml:"usb_index,omitempty"`  // For RTL-SDR
	Serial      string `yaml:"serial,omitempty"`     // For HackRF
	DefaultFreq uint64 `yaml:"default_freq"`         // Hz
	DefaultGain float64 `yaml:"default_gain"`        // dB
	DefaultBW   uint32 `yaml:"default_bw"`           // Hz
	Mode        string `yaml:"mode"`                 // "scanner" | "monitor" | "both"

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
		case "rtlsdr", "hackrf":
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
		if (s.Lat == nil) != (s.Lon == nil) {
			return nil, fmt.Errorf("sdr[%d]: lat and lon must be set together", i)
		}
	}
	return &cfg, nil
}