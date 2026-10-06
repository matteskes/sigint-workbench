// Package settings — §20 store: reads the per-service YAML configs
// into display snapshots and writes validated edits back in place
// (backup + atomic rename). The gateway is the only service with a
// writable config mount (SPEC §20).
package settings

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"sigint-workbench/internal/config"
	"sigint-workbench/internal/sdr"
)

// ErrUnknownSection is returned by Save for an unregistered section.
var ErrUnknownSection = errors.New("settings: unknown section")

// ValidationError marks a value the service config rules would
// reject. Field is the offending dotted key (or sdrs[i].key).
type ValidationError struct {
	Field string
	Err   error
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Err.Error() }

// Unwrap returns the underlying error.
func (e *ValidationError) Unwrap() error { return e.Err }

// SaveResult reports what was written and which services to restart.
type SaveResult struct {
	Section string   `json:"section"`
	File    string   `json:"file"`
	Restart []string `json:"restart"`
}

// Store reads and writes the YAML configs in dir on behalf of the
// §20 setup screen.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at dir (the services config dir).
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir returns the config directory root.
func (st *Store) Dir() string { return st.dir }

// Available reports whether the config directory exists and is
// writable by this process (in compose: the gateway rw mount).
func (st *Store) Available() error {
	probe := filepath.Join(st.dir, ".setup-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("settings: config dir %s not writable: %w", st.dir, err)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}

func sectionByID(id string) (Section, bool) {
	for _, s := range Schema() {
		if s.ID == id {
			return s, true
		}
	}
	return Section{}, false
}

// Snapshot returns display values per section ID. A section whose
// file is missing or unparseable maps to nil so the UI can show it
// as unavailable instead of silently showing stale defaults.
func (st *Store) Snapshot() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, sec := range Schema() {
		data, err := os.ReadFile(filepath.Join(st.dir, sec.File))
		if err != nil {
			out[sec.ID] = nil
			continue
		}
		vals, err := sectionValues(sec.ID, data)
		if err != nil {
			out[sec.ID] = nil
			continue
		}
		out[sec.ID] = vals
	}
	return out
}

// sectionValues decodes one section file against its typed config
// and returns display values keyed by the schema field keys.
func sectionValues(sectionID string, data []byte) (map[string]any, error) {
	switch sectionID {
	case "capture":
		return captureValues(data)
	case "processing":
		return processingValues(data)
	case "recorder":
		return recorderValues(data)
	case "classifier":
		return classifierValues(data)
	case "ingest":
		return ingestValues(data)
	}
	return nil, fmt.Errorf("settings: unknown section %q", sectionID)
}

// captureValues mirrors what sdr.LoadCaptureConfig resolves at load
// time: implicit stream host/port/mode defaults are shown as the
// values the loader will actually use.
func captureValues(data []byte) (map[string]any, error) {
	var cfg sdr.CaptureConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	v := map[string]any{}
	if cfg.StreamFormat != "" {
		v["stream_format"] = cfg.StreamFormat
	}
	v["scan.step_hz"] = float64(cfg.Scan.Step())
	v["scan.dwell_ms"] = float64(cfg.Scan.Dwell().Milliseconds())
	v["scan.min_hz"] = float64(cfg.Scan.MinHz)
	v["scan.max_hz"] = float64(cfg.Scan.MaxHz)
	devices := make([]map[string]any, 0, len(cfg.SDRs))
	for i, d := range cfg.SDRs {
		host := d.StreamHost
		if host == "" {
			host = "localhost"
		}
		port := d.StreamPort
		if port == 0 {
			port = 9000 + i
		}
		mode := d.Mode
		if mode == "" {
			mode = "monitor"
		}
		m := map[string]any{
			"id": d.ID, "driver": d.Driver,
			"default_freq": float64(d.DefaultFreq),
			"default_gain": d.DefaultGain,
			"default_bw":   float64(d.DefaultBW),
			"mode":         mode,
			"stream_host":  host,
			"stream_port":  float64(port),
		}
		if d.USBIndex != 0 {
			m["usb_index"] = float64(d.USBIndex)
		}
		if d.Serial != "" {
			m["serial"] = d.Serial
		}
		if d.CalibrationOffsetDB != nil {
			m["calibration_offset_db"] = *d.CalibrationOffsetDB
		}
		if d.Lat != nil {
			m["lat"] = *d.Lat
		}
		if d.Lon != nil {
			m["lon"] = *d.Lon
		}
		devices = append(devices, m)
	}
	v["sdrs"] = devices
	return v, nil
}

// processingValues mirrors signal-processor main: TDOA zero values
// take the withDefaults engine defaults (§9.6); the spectrum tap
// resolves bins 256 / rate 5 / enabled true (§18.4).
func processingValues(data []byte) (map[string]any, error) {
	var cfg config.SignalProcessorConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return map[string]any{
		"peak_detection.threshold_db":     cfg.PeakDetection.ThresholdDB,
		"peak_detection.min_spacing_bins": float64(cfg.PeakDetection.MinSpacingBins),
		"peak_detection.max_peaks":        float64(cfg.PeakDetection.MaxPeaks),
		"fft.size":                        float64(cfg.FFT.Size),
		"fft.window":                      cfg.FFT.Window,
		"tdoa.enabled":                    cfg.TDOA.Enabled,
		"tdoa.window_ms":                  float64(config.ResolveInt(cfg.TDOA.WindowMS, 10)),
		"tdoa.buffer_horizon_ms":          float64(config.ResolveInt(cfg.TDOA.BufferHorizonMS, 500)),
		"tdoa.pair_cap":                   float64(config.ResolveInt(cfg.TDOA.PairCap, 15)),
		"tdoa.outlier_sigma":              config.ResolveFloat(cfg.TDOA.OutlierSigma, 3),
		"tdoa.accuracy_budget_m":          config.ResolveFloat(cfg.TDOA.AccuracyBudgetM, 200),
		"tdoa.overwrite_placement":        cfg.TDOA.OverwritePlacement,
		"tdoa.locus_radius_m":             cfg.TDOA.LocusRadiusM,
		"tdoa.solve_rate_hz":              cfg.TDOA.SolveRateHz,
		"spectrum.enabled":                cfg.Spectrum.Enabled == nil || *cfg.Spectrum.Enabled,
		"spectrum.bins":                   float64(config.ResolveInt(cfg.Spectrum.Bins, 256)),
		"spectrum.rate_hz":                config.ResolveFloat(cfg.Spectrum.RateHz, 5),
	}, nil
}

// recorderValues shows iq.max_duration as the effective cap (0 means
// the §11.1 default 300; a negative value disables the cap) and the
// TFR pointer defaults (§19.5: disabled when the block is absent).
func recorderValues(data []byte) (map[string]any, error) {
	var cfg config.RecorderConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return map[string]any{
		"audio.sample_rate":       float64(cfg.Audio.SampleRate),
		"audio.channels":          float64(cfg.Audio.Channels),
		"iq.enabled":              cfg.IQ.Enabled,
		"iq.max_duration_s":       defaultFloat(float64(cfg.IQ.MaxDurationS), 300),
		"capture.close_silence_s": float64(cfg.Capture.CloseSilenceS),
		"capture.max_concurrent":  float64(cfg.Capture.MaxConcurrent),
		"retention.max_age_days":  float64(cfg.Retention.MaxAgeDays),
		"retention.max_size_gb":   cfg.Retention.MaxSizeGB,
		"stream.bitrate_bps":      float64(cfg.Stream.BitrateBps),
		"tfr.enabled":             cfg.TFR.Enabled != nil && *cfg.TFR.Enabled,
		"tfr.max_span_s":          config.ResolveFloat(cfg.TFR.MaxSpanS, 30),
		"tfr.max_nfft":            float64(config.ResolveInt(cfg.TFR.MaxNFFT, 16384)),
	}, nil
}

func classifierValues(data []byte) (map[string]any, error) {
	var cfg config.ClassifierConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return map[string]any{
		"rules.enabled":       cfg.Rules.Enabled,
		"onnx.enabled":        cfg.ONNX.Enabled,
		"onnx.min_confidence": cfg.ONNX.MinConfidence,
	}, nil
}

func ingestValues(data []byte) (map[string]any, error) {
	var cfg config.IQIngestConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return map[string]any{
		"stats.interval_s": float64(cfg.Stats.IntervalS),
	}, nil
}

// Save validates and applies values for one section: node edit, then
// re-marshal, then typed validation against the service configs,
// then backup + atomic rename. On any error nothing is committed.
func (st *Store) Save(sectionID string, values map[string]any) (*SaveResult, error) {
	sec, ok := sectionByID(sectionID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSection, sectionID)
	}
	if err := st.Available(); err != nil {
		return nil, err
	}
	path := filepath.Join(st.dir, sec.File)
	orig, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("settings: read %s: %w", sec.File, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(orig, &doc); err != nil {
		return nil, fmt.Errorf("settings: parse %s: %w", sec.File, err)
	}
	root := &doc
	if len(root.Content) > 0 {
		root = root.Content[0]
	} else {
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content = append(doc.Content, root)
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("settings: %s: top level is not a mapping", sec.File)
	}
	if err := applyValues(root, sec, values); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("settings: re-marshal %s: %w", sec.File, err)
	}
	if err := validateTyped(sec.ID, out); err != nil {
		return nil, err
	}
	// Keep the previous version (§20: cheap undo for hand recovery).
	if err := os.WriteFile(path+".bak", orig, 0o644); err != nil {
		return nil, fmt.Errorf("settings: backup %s: %w", sec.File, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return nil, fmt.Errorf("settings: write %s: %w", sec.File, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("settings: replace %s: %w", sec.File, err)
	}
	return &SaveResult{Section: sec.ID, File: sec.File, Restart: sec.Restart}, nil
}

// applyValues coerces and applies one section of submitted values.
// Optional strings model presence semantics: the empty string removes
// the key (e.g. stream_format absent = sdr1 default).
func applyValues(root *yaml.Node, sec Section, values map[string]any) error {
	for key, raw := range values {
		if lf := listField(sec, key); lf != nil {
			items, err := coerceList(*lf, raw)
			if err != nil {
				return err
			}
			if err := setList(root, lf.Key, items, listFieldOrder(*lf)); err != nil {
				return &ValidationError{Field: lf.Key, Err: err}
			}
			continue
		}
		f, ok := fieldByKey(sec, key)
		if !ok {
			return &ValidationError{Field: key,
				Err: fmt.Errorf("unknown setting for section %s", sec.ID)}
		}
		v, err := coerceScalar(f, raw)
		if err != nil {
			return &ValidationError{Field: key, Err: err}
		}
		if s, isStr := v.(string); isStr && s == "" && f.Optional {
			removeKey(root, f.Key)
			continue
		}
		if err := setScalar(root, f.Key, v); err != nil {
			return &ValidationError{Field: key, Err: err}
		}
	}
	return nil
}

// coerceScalar converts a JSON-decoded value to the field type and
// checks the declared bounds. encoding/json decodes numbers as
// float64, so that is the input domain here.
func coerceScalar(f Field, v any) (any, error) {
	switch f.Type {
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, errors.New("want true or false")
		}
		return b, nil
	case "select", "string":
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("want a string")
		}
		if f.Options != nil && s != "" && !contains(f.Options, s) {
			return nil, fmt.Errorf("want one of: %s", strings.Join(f.Options, ", "))
		}
		return s, nil
	default:
		n, ok := v.(float64)
		if !ok {
			return nil, errors.New("want a number")
		}
		if f.Min != nil && n < *f.Min {
			return nil, fmt.Errorf("must be at least %g", *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return nil, fmt.Errorf("must be at most %g", *f.Max)
		}
		if f.Integer && n != math.Trunc(n) {
			return nil, errors.New("want a whole number")
		}
		if f.Integer {
			return int64(n), nil
		}
		return n, nil
	}
}

// coerceList converts a JSON-decoded list into typed rows, rejecting
// unknown keys and missing required item fields.
func coerceList(lf ListField, v any) ([]map[string]any, error) {
	var arr []any
	switch items := v.(type) {
	case []any:
		arr = items
	case []map[string]any:
		arr = make([]any, len(items))
		for i, it := range items {
			arr[i] = it
		}
	default:
		return nil, listErr(lf.Key, 0, "", errors.New("want a list of devices"))
	}
	out := make([]map[string]any, 0, len(arr))
	for i, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, listErr(lf.Key, i, "", errors.New("want an object"))
		}
		row := map[string]any{}
		for k, raw := range m {
			f, ok := itemField(lf, k)
			if !ok {
				return nil, listErr(lf.Key, i, k, errors.New("unknown field"))
			}
			cv, err := coerceScalar(f, raw)
			if err != nil {
				return nil, listErr(lf.Key, i, k, err)
			}
			row[k] = cv
		}
		for _, f := range lf.ItemFields {
			if f.Optional {
				continue
			}
			if _, present := row[f.Key]; !present {
				return nil, listErr(lf.Key, i, f.Key, errors.New("required"))
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// listErr builds a ValidationError with an sdrs[i].field locator.
func listErr(key string, i int, field string, err error) error {
	f := key + "[" + strconv.Itoa(i) + "]"
	if field != "" {
		f += "." + field
	}
	return &ValidationError{Field: f, Err: err}
}

func fieldByKey(sec Section, key string) (Field, bool) {
	for _, f := range sec.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

func listField(sec Section, key string) *ListField {
	for i := range sec.Lists {
		if sec.Lists[i].Key == key {
			return &sec.Lists[i]
		}
	}
	return nil
}

func itemField(lf ListField, key string) (Field, bool) {
	for _, f := range lf.ItemFields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

func listFieldOrder(lf ListField) []string {
	order := make([]string, 0, len(lf.ItemFields))
	for _, f := range lf.ItemFields {
		order = append(order, f.Key)
	}
	return order
}

func contains(opts []string, s string) bool {
	for _, o := range opts {
		if o == s {
			return true
		}
	}
	return false
}

func defaultFloat(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}
