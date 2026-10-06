package settings

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"sigint-workbench/internal/config"
	"sigint-workbench/internal/sdr"
)

// validateTyped re-decodes the marshalled YAML into the same typed
// configs the services load and applies their domain rules, so a
// save can never produce a file a service would reject at startup.
func validateTyped(sectionID string, data []byte) error {
	switch sectionID {
	case "capture":
		var cfg sdr.CaptureConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return &ValidationError{Field: sectionID, Err: err}
		}
		return validateCapture(&cfg)
	case "processing":
		var cfg config.SignalProcessorConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return &ValidationError{Field: sectionID, Err: err}
		}
		return validateProcessing(&cfg)
	case "recorder":
		var cfg config.RecorderConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return &ValidationError{Field: sectionID, Err: err}
		}
		return validateRecorder(&cfg)
	case "classifier":
		var cfg config.ClassifierConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return &ValidationError{Field: sectionID, Err: err}
		}
		return validateClassifier(&cfg)
	case "ingest":
		var cfg config.IQIngestConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return &ValidationError{Field: sectionID, Err: err}
		}
		return validateIngest(&cfg)
	}
	return fmt.Errorf("settings: unknown section %q", sectionID)
}

func verr(field, format string, args ...any) error {
	return &ValidationError{Field: field, Err: fmt.Errorf(format, args...)}
}

func isPowerOfTwo(n int) bool { return n > 0 && n&(n-1) == 0 }

// validateCapture mirrors the sdr.LoadCaptureConfig rules and adds
// cross-device sanity (unique IDs, port range).
func validateCapture(cfg *sdr.CaptureConfig) error {
	if len(cfg.SDRs) == 0 {
		return verr("sdrs", "at least one SDR is required")
	}
	ids := map[string]bool{}
	for i, d := range cfg.SDRs {
		at := func(k string) string { return fmt.Sprintf("sdrs[%d].%s", i, k) }
		if d.ID == "" {
			return verr(at("id"), "required")
		}
		if ids[d.ID] {
			return verr(at("id"), "duplicate id %q", d.ID)
		}
		ids[d.ID] = true
		switch d.Driver {
		case "rtlsdr", "hackrf", "simulator":
		default:
			return verr(at("driver"), "unknown driver %q", d.Driver)
		}
		if d.DefaultFreq == 0 {
			return verr(at("default_freq"), "required")
		}
		if (d.Lat == nil) != (d.Lon == nil) {
			return verr(at("lat"), "lat and lon must be set together")
		}
		switch d.Mode {
		case "scanner", "monitor", "both":
		default:
			return verr(at("mode"), "unknown mode %q", d.Mode)
		}
		if d.StreamPort < 1 || d.StreamPort > 65535 {
			return verr(at("stream_port"), "out of range 1-65535")
		}
	}
	if cfg.Scan.MinHz != 0 && cfg.Scan.MaxHz != 0 && cfg.Scan.MinHz >= cfg.Scan.MaxHz {
		return verr("scan.min_hz", "must be below scan.max_hz")
	}
	switch cfg.StreamFormat {
	case "", "sdr1", "sdr2":
	default:
		return verr("stream_format", "unknown format %q", cfg.StreamFormat)
	}
	return nil
}

// validateProcessing enforces §5.7 FFT geometry, the §18.1 bins/
// size divisibility and §9.6 window/horizon coverage.
func validateProcessing(cfg *config.SignalProcessorConfig) error {
	if !isPowerOfTwo(cfg.FFT.Size) || cfg.FFT.Size < 64 || cfg.FFT.Size > 16384 {
		return verr("fft.size", "must be a power of two in 64-16384 (got %d)", cfg.FFT.Size)
	}
	switch cfg.FFT.Window {
	case "rectangular", "hann", "hamming", "blackman":
	default:
		return verr("fft.window", "unknown window %q", cfg.FFT.Window)
	}
	if cfg.PeakDetection.MinSpacingBins < 1 {
		return verr("peak_detection.min_spacing_bins", "must be at least 1")
	}
	if cfg.PeakDetection.MaxPeaks < 1 {
		return verr("peak_detection.max_peaks", "must be at least 1")
	}
	if cfg.Spectrum.Bins > 0 && cfg.FFT.Size%cfg.Spectrum.Bins != 0 {
		return verr("spectrum.bins", "%d does not divide fft.size %d (§18.1)", cfg.Spectrum.Bins, cfg.FFT.Size)
	}
	if cfg.TDOA.BufferHorizonMS != 0 && cfg.TDOA.BufferHorizonMS < cfg.TDOA.WindowMS {
		return verr("tdoa.buffer_horizon_ms", "horizon must cover the window")
	}
	if cfg.TDOA.PairCap != 0 && cfg.TDOA.PairCap < 2 {
		return verr("tdoa.pair_cap", "must be at least 2 (0 = default)")
	}
	return nil
}

// validateRecorder applies the §10/§11/§19 bounds; zero-valued
// fields that main resolves to defaults stay legal.
func validateRecorder(cfg *config.RecorderConfig) error {
	if cfg.Audio.SampleRate != 0 && (cfg.Audio.SampleRate < 8000 || cfg.Audio.SampleRate > 192000) {
		return verr("audio.sample_rate", "out of range 8000-192000")
	}
	if cfg.Audio.Channels != 0 && (cfg.Audio.Channels < 1 || cfg.Audio.Channels > 2) {
		return verr("audio.channels", "must be 1 or 2")
	}
	if cfg.Capture.CloseSilenceS != 0 && cfg.Capture.CloseSilenceS < 1 {
		return verr("capture.close_silence_s", "must be at least 1")
	}
	if cfg.Capture.MaxConcurrent != 0 && (cfg.Capture.MaxConcurrent < 1 || cfg.Capture.MaxConcurrent > 64) {
		return verr("capture.max_concurrent", "out of range 1-64")
	}
	if cfg.Stream.BitrateBps != 0 && (cfg.Stream.BitrateBps < 6000 || cfg.Stream.BitrateBps > 510000) {
		return verr("stream.bitrate_bps", "out of range 6000-510000")
	}
	if cfg.TFR.MaxNFFT != 0 && (!isPowerOfTwo(cfg.TFR.MaxNFFT) || cfg.TFR.MaxNFFT < 256 || cfg.TFR.MaxNFFT > 65536) {
		return verr("tfr.max_nfft", "must be a power of two in 256-65536 (§19.2)")
	}
	if cfg.TFR.MaxSpanS < 0 {
		return verr("tfr.max_span_s", "must be positive")
	}
	return nil
}

// validateClassifier: min_confidence is a 0..1 fraction (§8.3).
func validateClassifier(cfg *config.ClassifierConfig) error {
	if cfg.ONNX.MinConfidence < 0 || cfg.ONNX.MinConfidence > 1 {
		return verr("onnx.min_confidence", "must be within 0-1")
	}
	return nil
}

// validateIngest applies the stats reporting bounds (§16.1); the
// buffer_size knob was removed with the fictional fan-out queue (A2).
func validateIngest(cfg *config.IQIngestConfig) error {
	if cfg.Stats.IntervalS != 0 && cfg.Stats.IntervalS < 1 {
		return verr("stats.interval_s", "must be at least 1")
	}
	return nil
}
