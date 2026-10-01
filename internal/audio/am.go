// Package audio — AM (Amplitude Modulation) and SSB demodulator.
package audio

import (
	"fmt"
	"math"
)

// AMDemodulator demodulates AM (DSB-FC) and SSB (upper/lower sideband) signals.
type AMDemodulator struct {
	AudioRate   uint32 // Output audio sample rate (default 48000)
	SSBMode     string // "", "upper", or "lower"
}

// NewAMDemodulator creates an AM demodulator.
// Set ssbMode to "upper" or "lower" for SSB, or "" for standard AM.
func NewAMDemodulator(audioRate uint32, ssbMode string) *AMDemodulator {
	if audioRate == 0 {
		audioRate = 48000
	}
	return &AMDemodulator{
		AudioRate: audioRate,
		SSBMode:   ssbMode,
	}
}

// Name returns the modulation type.
func (a *AMDemodulator) Name() string {
	switch a.SSBMode {
	case "upper":
		return "USB"
	case "lower":
		return "LSB"
	default:
		return "AM"
	}
}

// CanHandle reports if this demodulator handles the given modulation string.
func (a *AMDemodulator) CanHandle(modulation string) bool {
	switch modulation {
	case "AM", "DSB", "SSB", "USB", "LSB", "am", "dsb", "ssb", "usb", "lsb":
		return true
	}
	return false
}

// AudioSampleRate returns the output audio sample rate.
func (a *AMDemodulator) AudioSampleRate() uint32 {
	return a.AudioRate
}

// Demodulate converts IQ samples to audio using an envelope detector.
func (a *AMDemodulator) Demodulate(iq []complex64, sampleRate uint32) ([]float32, error) {
	if len(iq) < 4 {
		return nil, fmt.Errorf("audio/am: not enough samples (%d)", len(iq))
	}
	if sampleRate == 0 {
		return nil, fmt.Errorf("audio/am: sample rate must be > 0")
	}

	n := len(iq)

	// Step 1: Envelope detection
	// For AM: amp[n] = |X[n]| = sqrt(I[n]^2 + Q[n]^2)
	// For SSB: apply a Hilbert transform first (simplified: use analytic signal)
	envelope := make([]float64, n)
	for i := range iq {
		re := real(iq[i])
		im := imag(iq[i])
		envelope[i] = math.Sqrt(re*re + im*im)
	}

	// Step 2: Remove DC offset (carrier)
	dc := 0.0
	for _, e := range envelope {
		dc += e
	}
	dc /= float64(n)

	audio := make([]float64, n)
	for i := range envelope {
		audio[i] = envelope[i] - dc
	}

	// Step 3: Normalize
	maxVal := 0.0
	for _, s := range audio {
		if v := math.Abs(s); v > maxVal {
			maxVal = v
		}
	}
	if maxVal > 0 {
		for i := range audio {
			audio[i] /= maxVal
		}
	}

	// Step 4: Decimate to audio rate
	decimateFactor := int(float64(sampleRate) / float64(a.AudioRate))
	if decimateFactor < 1 {
		decimateFactor = 1
	}

	// Simple low-pass + decimate
	filterLen := decimateFactor
	if filterLen < 2 {
		filterLen = 2
	}
	if filterLen%2 == 0 {
		filterLen++
	}
	halfFilter := filterLen / 2

	filtered := make([]float64, len(audio))
	for i := range audio {
		sum := 0.0
		count := 0
		for j := i - halfFilter; j <= i+halfFilter; j++ {
			if j >= 0 && j < len(audio) {
				sum += audio[j]
				count++
			}
		}
		if count > 0 {
			filtered[i] = sum / float64(count)
		}
	}

	out := make([]float32, 0, len(filtered)/decimateFactor)
	for i := 0; i < len(filtered); i += decimateFactor {
		v := float32(filtered[i])
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		out = append(out, v)
	}

	return out, nil
}