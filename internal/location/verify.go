// Package location — two-SDR cross-verification.
package location

import (
	"fmt"
	"time"
)

// Verifier performs two-SDR cross-verification of detected signals.
type Verifier struct {
	// MaxTimeDiff is the maximum time difference between the two
	// SDR observations for them to be considered a match.
	MaxTimeDiff time.Duration
}

// NewVerifier creates a verifier with default settings.
func NewVerifier() *Verifier {
	return &Verifier{
		MaxTimeDiff: 2 * time.Second,
	}
}

// Verify checks whether two SDRs detected the same signal.
// sdr1Freq and sdr2Freq are the frequencies each SDR was tuned to.
// sdr1Power and sdr2Power are the measured signal powers in dB.
func (v *Verifier) Verify(signalID string, sdr1ID, sdr2ID string,
	sdr1Freq, sdr2Freq uint64, sdr1Power, sdr2Power float64,
	sdr1Time, sdr2Time time.Time) (*Verification, error) {

	if sdr1ID == sdr2ID {
		return nil, fmt.Errorf("location: cannot verify with same SDR")
	}

	// Frequency match (within 5 kHz)
	freqDiff := float64(sdr1Freq) - float64(sdr2Freq)
	if freqDiff < 0 {
		freqDiff = -freqDiff
	}
	if freqDiff > 5000 {
		return &Verification{
			SignalID:   signalID,
			Verified:   false,
			SDR1:       sdr1ID,
			SDR2:       sdr2ID,
			Confidence: 0.0,
			Timestamp:  time.Now(),
		}, nil
	}

	// Time correlation
	timeDiff := sdr1Time.Sub(sdr2Time)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}
	if timeDiff > v.MaxTimeDiff {
		return &Verification{
			SignalID:   signalID,
			Verified:   false,
			SDR1:       sdr1ID,
			SDR2:       sdr2ID,
			Confidence: 0.2,
			Timestamp:  time.Now(),
		}, nil
	}

	// Power correlation (within 6 dB)
	powerDiff := sdr1Power - sdr2Power
	if powerDiff < 0 {
		powerDiff = -powerDiff
	}

	confidence := 0.9
	if powerDiff > 3.0 {
		confidence -= 0.2
	}
	if powerDiff > 6.0 {
		confidence -= 0.3
	}
	if confidence < 0 {
		confidence = 0
	}

	return &Verification{
		SignalID:   signalID,
		Verified:   confidence >= 0.5,
		SDR1:       sdr1ID,
		SDR2:       sdr2ID,
		Confidence: confidence,
		Timestamp:  time.Now(),
	}, nil
}