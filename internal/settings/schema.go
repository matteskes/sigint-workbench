// Package settings implements the §20 setup screen store: it reads the
// per-service YAML configs into a documented, typed schema for the
// browser, and writes edits back in place — comment-preserving
// (yaml.Node round-trip), validated against the same typed configs the
// services load, and committed atomically (tmp + rename, previous
// version kept as .bak). Services read their YAML once at startup, so
// saved settings take effect on restart (§20, D11).
package settings

// Field describes one editable scalar setting.
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Group    string   `json:"group"`
	Unit     string   `json:"unit,omitempty"`
	Step     *float64 `json:"step,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Options  []string `json:"options,omitempty"`
	Default  any      `json:"default,omitempty"`
	Integer  bool     `json:"integer,omitempty"`
	Optional bool     `json:"optional,omitempty"`
	Help     string   `json:"help,omitempty"`
	Warning  string   `json:"warning,omitempty"`
}

// ListField describes one editable YAML list (the SDR devices).
type ListField struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	ItemLabelKey string  `json:"itemLabelKey"`
	Help         string  `json:"help,omitempty"`
	ItemFields   []Field `json:"itemFields"`
}

// Section is one editable config file.
type Section struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	File        string      `json:"file"`
	Description string      `json:"description"`
	Restart     []string    `json:"restart"`
	Fields      []Field     `json:"fields"`
	Lists       []ListField `json:"lists,omitempty"`
}

func f64(v float64) *float64 { return &v }

// Schema returns the §20 editable surface. Deployment wiring (listen
// ports, dirs, URLs, compose env) is deliberately NOT exposed: those
// values must change in lockstep with docker-compose/.env and cannot
// be applied from the browser — see SPEC §20.
func Schema() []Section {
	return []Section{
		{
			ID:    "capture",
			Label: "Receivers & Scan",
			File:  "sdr-capture.yaml",
			Description: "SDR hardware inventory, per-device defaults and the " +
				"wideband scan loop (D3, §7.1).",
			Restart: []string{"sdr-capture", "signal-processor"},
			Lists: []ListField{
				{
					Key: "sdrs", Label: "SDR devices", ItemLabelKey: "id",
					Help: "One entry per receiver. Leave calibration_offset_db " +
						"empty until a §6.2 rtl-calibrate run — its presence is " +
						"what marks the device calibrated.",
					ItemFields: []Field{
						{Key: "id", Label: "ID", Type: "string", Group: "Device",
							Help: "Logical name, e.g. rtlsdr-0."},
						{Key: "driver", Label: "Driver", Type: "select", Group: "Device",
							Options: []string{"rtlsdr", "hackrf", "simulator"}, Default: "rtlsdr",
							Help: "hackrf needs a -tags rtlsdr,hackrf build."},
						{Key: "usb_index", Label: "USB index", Type: "number", Integer: true,
							Group: "Device", Min: f64(0), Max: f64(255), Optional: true,
							Help: "RTL-SDR only; rtl-list shows the index. Empty = 0."},
						{Key: "serial", Label: "Serial", Type: "string", Group: "Device",
							Optional: true, Help: "HackRF only; empty = first found."},
						{Key: "default_freq", Label: "Default frequency", Type: "number",
							Integer: true, Unit: "Hz", Group: "Defaults", Min: f64(1e6), Default: 100000000,
							Help: "Tune applied at startup."},
						{Key: "default_gain", Label: "Default gain", Type: "number",
							Unit: "dB", Group: "Defaults", Min: f64(0), Max: f64(90), Default: 20},
						{Key: "default_bw", Label: "Bandwidth", Type: "number", Integer: true,
							Unit: "Hz", Group: "Defaults", Min: f64(1), Default: 2400000,
							Help: "RTL-SDR practical max ~2.4 MHz."},
						{Key: "mode", Label: "Mode", Type: "select", Group: "Defaults",
							Options: []string{"scanner", "monitor", "both"}, Default: "monitor",
							Help: "scanner sweeps (§7.1); monitor holds one frequency."},
						{Key: "stream_host", Label: "Stream host", Type: "string",
							Group: "Stream", Default: "localhost",
							Help: "iq-ingest host (compose: iq-ingest)."},
						{Key: "stream_port", Label: "Stream port", Type: "number", Integer: true,
							Group: "Stream", Min: f64(1), Max: f64(65535), Default: 9000,
							Help: "iq-ingest UDP port (shipped default 9000)."},
						{Key: "calibration_offset_db", Label: "Calibration offset",
							Type: "number", Unit: "dB", Group: "Calibration", Optional: true,
							Help: "§5.6/§6.2: from bin/rtl-calibrate. Empty = " +
								"uncalibrated (relative dB).",
							Warning: "Re-run §6.2 calibration after gain or FFT " +
								"geometry changes."},
						{Key: "lat", Label: "Latitude", Type: "number", Unit: "deg",
							Group: "Location", Step: f64(0.0001), Optional: true,
							Help: "Receiver position for single-receiver " +
								"geolocation (§9.3). Set lat+lon together."},
						{Key: "lon", Label: "Longitude", Type: "number", Unit: "deg",
							Group: "Location", Step: f64(0.0001), Optional: true},
					},
				},
			},
			Fields: []Field{
				{Key: "stream_format", Label: "IQ wire format", Type: "select",
					Group: "Stream", Options: []string{"sdr1", "sdr2"}, Default: "sdr1", Optional: true,
					Help: "§4.5: v2 frames carry the sample-accurate timing " +
						"TDOA needs (§9.6). Empty = sdr1.",
					Warning: "Flip to sdr2 only once every consumer accepts v2."},
				{Key: "scan.step_hz", Label: "Sweep step", Type: "number", Integer: true,
					Unit: "Hz", Group: "Scan loop (D3)", Default: 100000, Min: f64(1000),
					Help: "0 = default 100 kHz."},
				{Key: "scan.dwell_ms", Label: "Dwell", Type: "number", Integer: true,
					Unit: "ms", Group: "Scan loop (D3)", Default: 50, Min: f64(1),
					Help: "0 = default 50 ms."},
				{Key: "scan.min_hz", Label: "Sweep start", Type: "number", Integer: true,
					Unit: "Hz", Group: "Scan loop (D3)", Optional: true,
					Help: "0 = driver minimum (RTL-SDR 24 MHz); clamped to the " +
						"capability range at startup."},
				{Key: "scan.max_hz", Label: "Sweep end", Type: "number", Integer: true,
					Unit: "Hz", Group: "Scan loop (D3)", Optional: true,
					Help: "0 = driver maximum (RTL-SDR 1766 MHz)."},
			},
		},
		{
			ID:    "processing",
			Label: "Detection & Processing",
			File:  "signal-processor.yaml",
			Description: "Peak detection, FFT geometry, scan mirror, the §9.6 " +
				"TDOA engine and the §18 spectrum tap.",
			Restart: []string{"signal-processor"},
			Fields: []Field{
				{Key: "peak_detection.threshold_db", Label: "Peak threshold",
					Type: "number", Unit: "dB", Group: "Peak detection",
					Help: "Bins above this count as peaks (shipped -60)."},
				{Key: "peak_detection.min_spacing_bins", Label: "Min spacing",
					Type: "number", Integer: true, Unit: "bins",
					Group: "Peak detection", Min: f64(1),
					Help: "Adjacent-peak merge distance."},
				{Key: "peak_detection.max_peaks", Label: "Max peaks",
					Type: "number", Integer: true, Group: "Peak detection",
					Min: f64(1), Max: f64(64),
					Help: "Shipped 8."},
				{Key: "fft.size", Label: "FFT size", Type: "number", Integer: true,
					Unit: "pairs", Group: "FFT (§5.7)", Min: f64(64), Max: f64(16384),
					Help: "IQ pairs per assembled buffer; one-sided bins = size/2 " +
						"at 1 kHz bin width for 48 kS/s half-bands.",
					Warning: "Power of two, 64-16384. Changing it (or the window) " +
						"invalidates §6.2 calibration offsets and the ONNX " +
						"training geometry — retrain + recalibrate."},
				{Key: "fft.window", Label: "FFT window", Type: "select",
					Group:   "FFT (§5.7)",
					Options: []string{"rectangular", "hann", "hamming", "blackman"},
					Help:    "rectangular matches the unwindowed ONNX training.",
					Warning: "Switching windows changes the power scale — retrain " +
						"the model and recalibrate."},
				{Key: "scan.step_hz", Label: "Sweep step", Type: "number", Integer: true,
					Unit: "Hz", Group: "Scan mirror", Default: 100000, Min: f64(1000),
					Help: "Keep in step with the capture scan loop."},
				{Key: "scan.dwell_ms", Label: "Dwell", Type: "number", Integer: true,
					Unit: "ms", Group: "Scan mirror", Default: 50, Min: f64(1)},
				{Key: "scan.min_freq_hz", Label: "Scan range start", Type: "number",
					Integer: true, Unit: "Hz", Group: "Scan mirror", Min: f64(0),
					Help: "Shipped 87.5 MHz. 0 = driver default."},
				{Key: "scan.max_freq_hz", Label: "Scan range end", Type: "number",
					Integer: true, Unit: "Hz", Group: "Scan mirror", Min: f64(0),
					Help: "Shipped 108 MHz. 0 = driver default."},
				{Key: "tdoa.enabled", Label: "TDOA engine", Type: "bool",
					Group: "TDOA (§9.6)", Default: false,
					Help: "Requires §4.5 sdr2 frames and receiver lat/lon."},
				{Key: "tdoa.window_ms", Label: "Observation window", Type: "number",
					Integer: true, Unit: "ms", Group: "TDOA (§9.6)", Default: 10,
					Help: "0 = default 10 ms."},
				{Key: "tdoa.buffer_horizon_ms", Label: "Buffer horizon", Type: "number",
					Integer: true, Unit: "ms", Group: "TDOA (§9.6)", Default: 500,
					Help: "0 = default 500 ms (§9.5)."},
				{Key: "tdoa.pair_cap", Label: "Pair cap", Type: "number", Integer: true,
					Group: "TDOA (§9.6)", Default: 15, Min: f64(2),
					Help: "All-pairs above the cap degrade to a star topology; " +
						"0 = default 15."},
				{Key: "tdoa.outlier_sigma", Label: "Outlier gate", Type: "number",
					Unit: "sigma", Group: "TDOA (§9.6)", Default: 3,
					Help: "Normalized-residual rejection gate; 0 = default 3."},
				{Key: "tdoa.accuracy_budget_m", Label: "Fix gate", Type: "number",
					Unit: "m", Group: "TDOA (§9.6)", Default: 200,
					Help: "Solutions wider than this are rejected; 0 = default 200."},
				{Key: "tdoa.overwrite_placement", Label: "Overwrite placement",
					Type: "bool", Group: "TDOA (§9.6)", Default: false,
					Help: "Covariance-gated overwrite of the §9.3 placement."},
				{Key: "tdoa.locus_radius_m", Label: "Locus radius", Type: "number",
					Unit: "m", Group: "TDOA (§9.6)", Min: f64(0),
					Help: "0 = 3x the pair baseline."},
				{Key: "tdoa.solve_rate_hz", Label: "Solve rate", Type: "number",
					Unit: "Hz", Group: "TDOA (§9.6)", Min: f64(0),
					Help: "0 = bandwidth-derived (250 kS/s default)."},
				{Key: "spectrum.enabled", Label: "Spectrum tap", Type: "bool",
					Group: "Spectrum (§18)", Default: true,
					Help: "false = zero events, zero tap allocation (§18.4)."},
				{Key: "spectrum.bins", Label: "Bins", Type: "number", Integer: true,
					Group: "Spectrum (§18)", Default: 256, Min: f64(1), Max: f64(512),
					Warning: "Must divide fft.size (§18.1) — e.g. 4096/256 = 16 " +
						"source bins per displayed bin."},
				{Key: "spectrum.rate_hz", Label: "Max frames/s", Type: "number",
					Unit: "Hz", Group: "Spectrum (§18)", Default: 5,
					Min: f64(0.1), Max: f64(100),
					Help: "A cap, not a guarantee (§18.1)."},
			},
		},
		{
			ID:    "recorder",
			Label: "Recorder & TFR",
			File:  "recorder.yaml",
			Description: "Audio demodulation, IQ recording, in-band capture " +
				"(D1), retention (§11), live Opus audio (§10.4) and the §19 " +
				"time-frequency engine.",
			Restart: []string{"recorder"},
			Fields: []Field{
				{Key: "audio.sample_rate", Label: "Audio sample rate", Type: "number",
					Integer: true, Unit: "Hz", Group: "Audio (§10.5)", Min: f64(8000), Max: f64(192000),
					Help: "Shipped 48000; applies to the demodulated audio path."},
				{Key: "audio.channels", Label: "Channels", Type: "number", Integer: true,
					Group: "Audio (§10.5)", Min: f64(1), Max: f64(2),
					Help: "Shipped 1 (mono)."},
				{Key: "iq.enabled", Label: "IQ recording", Type: "bool",
					Group: "IQ recording (§10.5)", Default: false,
					Help: "Second, headerless int16-LE file per session; required " +
						"for §19 time-frequency renders."},
				{Key: "iq.max_duration_s", Label: "Max session length", Type: "number",
					Integer: true, Unit: "s", Group: "IQ recording (§10.5)", Default: 300,
					Help: "§11.1 cap; negative disables the cap; 0 = default 300."},
				{Key: "capture.close_silence_s", Label: "Close after silence",
					Type: "number", Integer: true, Unit: "s", Group: "Capture (D1)",
					Min: f64(1), Help: "Shipped 10 s."},
				{Key: "capture.max_concurrent", Label: "Max concurrent recordings",
					Type: "number", Integer: true, Group: "Capture (D1)",
					Min: f64(1), Max: f64(64), Help: "Shipped 8."},
				{Key: "retention.max_age_days", Label: "Max age", Type: "number",
					Integer: true, Unit: "days", Group: "Retention (§11)", Min: f64(0),
					Help: "Shipped 30; 0 = no age limit."},
				{Key: "retention.max_size_gb", Label: "Max size", Type: "number",
					Unit: "GB", Group: "Retention (§11)", Min: f64(0),
					Help: "Shipped 50; 0 = no size limit."},
				{Key: "stream.bitrate_bps", Label: "Opus bitrate", Type: "number",
					Integer: true, Unit: "bps", Group: "Live audio (§10.4)",
					Min: f64(6000), Max: f64(510000),
					Help: "CBR, mono, 20 ms frames; shipped 24000."},
				{Key: "tfr.enabled", Label: "Time-frequency analysis", Type: "bool",
					Group: "TFR (§19)", Default: false,
					Help: "While disabled the §19.3 endpoint answers 404."},
				{Key: "tfr.max_span_s", Label: "Max span", Type: "number", Unit: "s",
					Group: "TFR (§19)", Default: 30, Min: f64(0.1), Max: f64(3600),
					Help: "Largest requestable span (§19.3 answers 413 beyond); " +
						"0 = default 30 s."},
				{Key: "tfr.max_nfft", Label: "Max nfft", Type: "number", Integer: true,
					Group: "TFR (§19)", Default: 16384, Min: f64(256), Max: f64(65536),
					Help: "Per-request nfft cap (§19.2); power of two; " +
						"0 = default 16384."},
			},
		},
		{
			ID:    "classifier",
			Label: "Classifier",
			File:  "classifier.yaml",
			Description: "Rules + ONNX classification (§8). The model file " +
				"itself is git-lfs and volume-mounted — train and swap it on " +
				"disk, not here.",
			Restart: []string{"signal-processor"},
			Fields: []Field{
				{Key: "rules.enabled", Label: "Rule-based fallback", Type: "bool",
					Group: "Rules", Default: true,
					Help: "Always-active fallback (§8.2)."},
				{Key: "onnx.enabled", Label: "ONNX classifier", Type: "bool",
					Group: "ONNX (§8.3)", Default: false,
					Help: "Needs the onnx-tagged build + model."},
				{Key: "onnx.min_confidence", Label: "Min confidence", Type: "number",
					Unit: "0-1", Group: "ONNX (§8.3)", Step: f64(0.05), Min: f64(0),
					Max: f64(1), Help: "Below this, fall back to rules (shipped 0.5)."},
			},
		},
		{
			ID:    "ingest",
			Label: "IQ Ingest",
			File:  "iq-ingest.yaml",
			Description: "UDP frame validation and fan-out (§6). Consumer " +
				"targets are compose wiring and stay in iq-ingest.yaml by hand.",
			Restart: []string{"iq-ingest"},
			Fields: []Field{
				{Key: "buffer_size", Label: "Fan-out buffer", Type: "number",
					Integer: true, Unit: "frames", Group: "Fan-out", Min: f64(16), Max: f64(65536),
					Help: "Per-consumer frame buffer (shipped 256); larger = more " +
						"burst absorption, more memory."},
				{Key: "stats.interval_s", Label: "Stats interval", Type: "number",
					Integer: true, Unit: "s", Group: "Stats", Min: f64(1),
					Help: "Shipped 5."},
			},
		},
	}
}
