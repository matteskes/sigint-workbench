// Package classify — rule-based signal classifier (no ML required).
//
// Uses frequency band, estimated bandwidth, and spectral shape
// to make a preliminary classification. This runs before the
// ONNX model and provides a fast fallback.
package classify

import (
	"sigint-workbench/internal/dsp"
)

// RuleClassifier classifies signals using frequency and bandwidth rules.
type RuleClassifier struct{}

// NewRuleClassifier creates a rule-based classifier.
func NewRuleClassifier() *RuleClassifier {
	return &RuleClassifier{}
}

// Classify makes a classification based on frequency and bandwidth.
func (rc *RuleClassifier) Classify(freqHz uint64, bandwidthHz float64, spectrum *dsp.FFTResult) *Result {
	band := dsp.IdentifyBand(freqHz)
	bandName := "Unknown"
	if band != nil {
		bandName = band.Name
	}

	modulation := "Unknown"
	subType := ""
	source := "unknown"
	confidence := 0.3 // low confidence for rule-based

	switch {
	// Aviation VHF airband (118-137 MHz): AM voice in 25 kHz (8.33
	// in Europe) channels (B4 — was FM/WFM at 0.85, confidently
	// mislabeling every airband-range noise peak). A voice-channel
	// bandwidth confirms at moderate confidence; anything wider, or
	// the single-bin spike estimateBandwidth reports as 0, falls
	// through to Unknown at the default 0.3.
	case freqHz >= 118_000_000 && freqHz <= 137_000_000:
		if bandwidthHz > 0 && bandwidthHz <= 40_000 {
			modulation = "AM"
			source = "aviation"
			confidence = 0.6
		}

	// Marine VHF (156-174 MHz)
	case freqHz >= 156_000_000 && freqHz <= 174_000_000:
		modulation = "FM"
		subType = "WFM"
		source = "marine"
		confidence = 0.85

	// VHF land mobile (146-174 MHz, 420-512 MHz)
	case (freqHz >= 146_000_000 && freqHz <= 174_000_000) ||
		(freqHz >= 420_000_000 && freqHz <= 512_000_000):
		if bandwidthHz > 15000 {
			modulation = "FM"
			subType = "WFM"
		} else {
			modulation = "FM"
			subType = "NFM"
		}
		source = "land_mobile"
		confidence = 0.7

	// HF (3-30 MHz) — likely SSB or CW
	case freqHz >= 3_000_000 && freqHz <= 30_000_000:
		if bandwidthHz < 3000 {
			modulation = "SSB"
			subType = "USB"
			source = "amateur"
		} else {
			modulation = "AM"
			source = "unknown"
		}
		confidence = 0.5

	// MF (300 kHz - 3 MHz) — AM broadcast
	case freqHz >= 530_000 && freqHz <= 1700_000:
		modulation = "AM"
		source = "broadcast"
		confidence = 0.9

	// FM broadcast (88-108 MHz)
	case freqHz >= 88_000_000 && freqHz <= 108_000_000:
		modulation = "FM"
		subType = "WFM"
		source = "broadcast"
		confidence = 0.95

	// GPS L1 (1575.42 MHz)
	case freqHz >= 1575_000_000 && freqHz <= 1576_000_000:
		modulation = "CDMA"
		source = "gnss"
		confidence = 0.95

	// Wi-Fi 2.4 GHz
	case freqHz >= 2400_000_000 && freqHz <= 2483_000_000:
		modulation = "OFDM"
		source = "wifi"
		confidence = 0.8
	}

	return &Result{
		Modulation: modulation,
		SubType:    subType,
		BandName:   bandName,
		Source:     source,
		Confidence: confidence,
		Method:     "rules",
		Bandwidth:  bandwidthHz,
		Frequency:  freqHz,
	}
}
