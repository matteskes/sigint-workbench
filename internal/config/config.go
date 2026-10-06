// Package config provides shared configuration loading.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads a YAML config file into the given struct.
func Load(path string, out interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	return nil
}

// ─── Typed service configs (§16) ─────────────────────────────────────

// ConsumerConfig is one iq-ingest fan-out target.
type ConsumerConfig struct {
	Name string `yaml:"name"`
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Addr returns the consumer's host:port dial address ("" if incomplete).
func (c ConsumerConfig) Addr() string {
	if c.Host == "" || c.Port == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// IQIngestConfig mirrors config/iq-ingest.yaml.
type IQIngestConfig struct {
	ListenPort int              `yaml:"listen_port"`
	Consumers  []ConsumerConfig `yaml:"consumers"`
	Stats      struct {
		IntervalS int `yaml:"interval_s"`
	} `yaml:"stats"`
}

// SignalProcessorConfig mirrors config/signal-processor.yaml.
type SignalProcessorConfig struct {
	ListenPort    int `yaml:"listen_port"`
	PeakDetection struct {
		ThresholdDB    float64 `yaml:"threshold_db"`
		MinSnrDB       float64 `yaml:"min_snr_db"`
		MinSpacingBins int     `yaml:"min_spacing_bins"`
		MaxPeaks       int     `yaml:"max_peaks"`
	} `yaml:"peak_detection"`
	FFT struct {
		Size   int    `yaml:"size"`
		Window string `yaml:"window"`
	} `yaml:"fft"`
	// TDOA engine (§9.6). Disabled by default; see the tdoa block in
	// config/signal-processor.yaml for the per-key semantics.
	TDOA TDOAConfig `yaml:"tdoa"`

	// Spectrum tap (§18): decimated per-SDR power spectra for the
	// dashboard spectrum/waterfall. Enabled defaults to true (*bool
	// nil), bins to 256, rate_hz to 5 (§18.4) — resolved in main.
	Spectrum SpectrumConfig `yaml:"spectrum"`
}

// SpectrumConfig holds the §18.4 spectrum-tap knobs. Enabled is a
// pointer so an absent yaml block keeps the spec default (true).
type SpectrumConfig struct {
	Enabled *bool   `yaml:"enabled"`
	Bins    int     `yaml:"bins"`
	RateHz  float64 `yaml:"rate_hz"`
}

// TDOAConfig holds the §9.6 engine knobs. Zero values take the
// documented defaults (applied by the engine constructor).
type TDOAConfig struct {
	Enabled            bool    `yaml:"enabled"`
	WindowMS           int     `yaml:"window_ms"`
	BufferHorizonMS    int     `yaml:"buffer_horizon_ms"`
	PairCap            int     `yaml:"pair_cap"`
	OutlierSigma       float64 `yaml:"outlier_sigma"`
	AccuracyBudgetM    float64 `yaml:"accuracy_budget_m"`
	OverwritePlacement bool    `yaml:"overwrite_placement"`
	LocusRadiusM       float64 `yaml:"locus_radius_m"`
	SolveRateHz        float64 `yaml:"solve_rate_hz"`
}

// ClassifierConfig mirrors config/classifier.yaml.
type ClassifierConfig struct {
	ModelPath string `yaml:"model_path"`
	Rules     struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"rules"`
	ONNX struct {
		Enabled       bool    `yaml:"enabled"`
		MinConfidence float64 `yaml:"min_confidence"`
	} `yaml:"onnx"`
}

// TFRConfig holds the §19.5 time-frequency knobs. Enabled is a
// pointer so an absent yaml block keeps the spec default (false —
// feature absent ⇒ 404, §19.3).
type TFRConfig struct {
	Enabled    *bool   `yaml:"enabled"`
	MaxSpanS   float64 `yaml:"max_span_s"`
	MaxNFFT    int     `yaml:"max_nfft"`
	ListenPort int     `yaml:"listen_port"` // internal TFR API (§19.3)
}

// RecorderConfig mirrors config/recorder.yaml.
type RecorderConfig struct {
	RecordingsDir string `yaml:"recordings_dir"`
	ListenPort    int    `yaml:"listen_port"`
	Audio         struct {
		SampleRate uint32 `yaml:"sample_rate"`
		Channels   int    `yaml:"channels"`
	} `yaml:"audio"`
	IQ struct {
		Enabled      bool `yaml:"enabled"`
		MaxDurationS int  `yaml:"max_duration_s"`
	} `yaml:"iq"`
	Retention struct {
		MaxAgeDays int     `yaml:"max_age_days"`
		MaxSizeGB  float64 `yaml:"max_size_gb"`
	} `yaml:"retention"`
	Capture struct {
		CloseSilenceS int `yaml:"close_silence_s"`
		MaxConcurrent int `yaml:"max_concurrent"`
	} `yaml:"capture"`
	Stream struct {
		ListenPort int `yaml:"listen_port"` // internal /ws/audio WS (§10.4)
		BitrateBps int `yaml:"bitrate_bps"` // Opus CBR bitrate (§10.4: 24000)
	} `yaml:"stream"`
	// Time-frequency analysis (§19, D10): on-demand high-resolution
	// renders over stored raw IQ. Disabled by default (§19.5).
	TFR TFRConfig `yaml:"tfr"`
}

// ─── Precedence helpers (flag > env > yaml > default) ────────────────

// ResolveInt returns the first non-zero value.
func ResolveInt(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// ResolveFloat returns the first non-zero value.
func ResolveFloat(values ...float64) float64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// ResolveString returns the first non-empty value.
func ResolveString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// GetEnv returns an environment variable or a default value.
func GetEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// GetEnvInt returns an integer environment variable or a default.
func GetEnvInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return defaultVal
	}
	return n
}
