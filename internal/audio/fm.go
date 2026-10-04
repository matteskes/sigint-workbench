// Package audio — FM (Wideband and Narrowband) demodulator.
package audio

import (
	"fmt"
	"math"
)

// FMDemodulator demodulates WFM (25 kHz deviation) and NFM (2.5/5 kHz deviation) signals.
type FMDemodulator struct {
	DeviationHz  float64 // Frequency deviation (25000 for WFM, 2500/5000 for NFM)
	AudioRate    uint32  // Output audio sample rate (default 48000)
	AudioCutoff  float64 // Audio low-pass cutoff in Hz (default 3400)
}

// NewFMDemodulator creates an FM demodulator for the given deviation.
// Use 25000 for WFM (aviation, land mobile), 2500 or 5000 for NFM (ham, PMR).
func NewFMDemodulator(deviationHz float64, audioRate uint32) *FMDemodulator {
	if audioRate == 0 {
		audioRate = 48000
	}
	return &FMDemodulator{
		DeviationHz: deviationHz,
		AudioRate:   audioRate,
		AudioCutoff: 3400,
	}
}

// Name returns "WFM" or "NFM" based on deviation.
func (f *FMDemodulator) Name() string {
	if f.DeviationHz >= 10000 {
		return "WFM"
	}
	return "NFM"
}

// CanHandle reports if this demodulator handles the given modulation string.
func (f *FMDemodulator) CanHandle(modulation string) bool {
	switch modulation {
	case "FM", "WFM", "NFM", "fm", "wfm", "nfm":
		return true
	}
	return false
}

// CanHandlePair reports whether this demodulator handles the
// (modulation, subType) pair. Wideband demods claim WFM (or FM with
// no subtype); narrowband demods claim NFM (§10.1 registry fix).
func (f *FMDemodulator) CanHandlePair(modulation, subType string) bool {
	mod := NormalizeModulation(modulation)
	sub := NormalizeModulation(subType)
	wide := f.DeviationHz >= 10000
	switch mod {
	case "WFM":
		return wide && (sub == "" || sub == "WFM")
	case "NFM":
		return !wide && (sub == "" || sub == "NFM")
	case "FM":
		if sub == "" {
			return true
		}
		return (sub == "WFM") == wide
	}
	return false
}

// AudioSampleRate returns the output audio sample rate.
func (f *FMDemodulator) AudioSampleRate() uint32 {
	return f.AudioRate
}

// Demodulate converts IQ samples to audio using an FM discriminator.
func (f *FMDemodulator) Demodulate(iq []complex64, sampleRate uint32) ([]float32, error) {
	if len(iq) < 4 {
		return nil, fmt.Errorf("audio/fm: not enough samples (%d)", len(iq))
	}
	if sampleRate == 0 {
		return nil, fmt.Errorf("audio/fm: sample rate must be > 0")
	}

	// Step 1: FM discriminator via phase difference
	// phase[n] = atan2(Q[n], I[n])
	// freq[n]  = (phase[n] - phase[n-1]) * sampleRate / (2*pi)
	// audio[n] = freq[n] / deviation
	n := len(iq)
	discriminated := make([]float64, n-1)
	for i := 1; i < n; i++ {
		re1, im1 := real(iq[i-1]), imag(iq[i-1])
		re2, im2 := real(iq[i]), imag(iq[i])

		// Cross-product phase difference (avoids atan2 for speed)
		cross := im2*re1-re2*im1
		dot := re2*re1 + im2*im1
		dPhase := math.Atan2(float64(cross), float64(dot))

		// Unwrap: phase difference should be small
		if dPhase > math.Pi {
			dPhase -= 2 * math.Pi
		} else if dPhase < -math.Pi {
			dPhase += 2 * math.Pi
		}

		freq := dPhase * float64(sampleRate) / (2 * math.Pi)
		discriminated[i-1] = freq / f.DeviationHz
	}

	// Step 2: Decimate to audio rate
	decimateFactor := int(float64(sampleRate) / float64(f.AudioRate))
	if decimateFactor < 1 {
		decimateFactor = 1
	}

	// Apply a simple moving-average low-pass before decimation
	filterLen := decimateFactor
	if filterLen < 2 {
		filterLen = 2
	}
	if filterLen%2 == 0 {
		filterLen++ // make odd for symmetric filter
	}
	halfFilter := filterLen / 2

	filtered := make([]float64, len(discriminated))
	for i := range discriminated {
		sum := 0.0
		count := 0
		for j := i - halfFilter; j <= i+halfFilter; j++ {
			if j >= 0 && j < len(discriminated) {
				sum += discriminated[j]
				count++
			}
		}
		if count > 0 {
			filtered[i] = sum / float64(count)
		}
	}

	// Decimate
	audio := make([]float64, 0, len(filtered)/decimateFactor)
	for i := 0; i < len(filtered); i += decimateFactor {
		audio = append(audio, filtered[i])
	}

	// Step 3: Convert to float32, clip
	out := make([]float32, len(audio))
	for i, s := range audio {
		v := float32(s)
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		out[i] = v
	}

	return out, nil
}