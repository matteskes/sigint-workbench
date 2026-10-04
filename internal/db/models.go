// Package db — database models.
package db

import "time"

// Signal represents a detected RF signal.
type Signal struct {
	ID          string  `json:"id" db:"id"`
	FreqHz      uint64  `json:"freqHz" db:"freq_hz"`
	BandwidthHz int32   `json:"bandwidthHz" db:"bandwidth_hz"`
	Modulation  string  `json:"modulation" db:"modulation"`
	SubType     string  `json:"subType" db:"sub_type"`
	Class       string  `json:"class" db:"class"`
	Method      string  `json:"method" db:"method"` // "rules" or "onnx"
	Confidence  float64 `json:"confidence" db:"confidence"`
	PowerDBM    float64 `json:"powerDbm" db:"power_dbm"`
	// §5.6: true only when power_dbm is calibrated (the detecting SDR
	// has a calibration_offset_db); false = relative dB (powerDbm holds
	// the raw relative value).
	PowerCalibrated bool      `json:"powerCalibrated" db:"power_calibrated"`
	Lat             *float64  `json:"lat" db:"lat"`
	Lon             *float64  `json:"lon" db:"lon"`
	AccuracyM       float64   `json:"accuracyM" db:"accuracy_m"`
	FirstSeen       time.Time `json:"firstSeen" db:"first_seen"`
	LastSeen        time.Time `json:"lastSeen" db:"last_seen"`
	SDRID           string    `json:"sdrId" db:"sdr_id"`
	Verified        bool      `json:"verified" db:"verified"`
	Active          bool      `json:"active" db:"active"` // §11.2 lifecycle
}

// Recording represents a recorded signal capture.
type Recording struct {
	ID          string    `json:"id" db:"id"`
	SignalID    string    `json:"signalId" db:"signal_id"`
	StartTime   time.Time `json:"startTime" db:"start_time"`
	EndTime     time.Time `json:"endTime" db:"end_time"`
	DurationS   float64   `json:"durationS" db:"duration_s"`
	SampleRate  int32     `json:"sampleRate" db:"sample_rate"`
	CenterFreq  uint64    `json:"centerFreq" db:"center_freq"`
	BandwidthHz int32     `json:"bandwidthHz" db:"bandwidth_hz"`
	FilePath    string    `json:"filePath" db:"file_path"`
	FileFormat  string    `json:"fileFormat" db:"file_format"`
	SizeBytes   int64     `json:"sizeBytes" db:"size_bytes"`
}

// SDRDevice represents a registered SDR.
type SDRDevice struct {
	ID     string  `json:"id" db:"id"`
	Model  string  `json:"model" db:"model"`
	Serial string  `json:"serial" db:"serial"`
	Lat    float64 `json:"lat" db:"lat"`
	Lon    float64 `json:"lon" db:"lon"`
	GainDB float64 `json:"gainDb" db:"gain_db"`
	FreqHz uint64  `json:"freqHz" db:"freq_hz"`
	Active bool    `json:"active" db:"active"`
}

// Verification represents a two-SDR cross-check result (§8).
type Verification struct {
	ID         string    `json:"id" db:"id"`
	SignalID   string    `json:"signalId" db:"signal_id"`
	SDR1       string    `json:"sdr1" db:"sdr1_id"`
	SDR2       string    `json:"sdr2" db:"sdr2_id"`
	Verified   bool      `json:"verified" db:"verified"`
	Confidence float64   `json:"confidence" db:"confidence"`
	CreatedAt  time.Time `json:"createdAt" db:"created_at"`
}

// Annotation represents a user note attached to a signal (§12.5).
type Annotation struct {
	ID        string    `json:"id" db:"id"`
	SignalID  string    `json:"signalId" db:"signal_id"`
	UserNote  string    `json:"userNote" db:"user_note"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}
