// Package audio — SSB (Single Sideband) demodulator.
//
// Implements true sideband selection for zero-IF complex baseband:
// the wanted sideband (positive offsets for USB, negative for LSB)
// is isolated in the frequency domain and inverted to a real audio
// signal. This replaces the legacy AM-envelope stand-in, which
// demodulated SSB as if it were AM (SPEC §10.1).
package audio

import (
	"fmt"
	"math"
	"math/cmplx"

	"gonum.org/v1/gonum/dsp/fourier"
)

// defaultVoiceBandwidthHz is the audio bandwidth kept for SSB voice.
const defaultVoiceBandwidthHz = 3000.0

// SSBDemodulator demodulates USB/LSB single-sideband signals.
type SSBDemodulator struct {
	AudioRate      uint32 // Output audio sample rate (default 48000)
	Sideband       string // "upper"/"usb" (USB) or "lower"/"lsb" (LSB)
	VoiceBandwidth float64 // Audio bandwidth in Hz (default 3000)
}

// NewSSBDemodulator creates an SSB demodulator. sideband is
// "upper"/"usb" for USB or "lower"/"lsb" for LSB.
func NewSSBDemodulator(audioRate uint32, sideband string) *SSBDemodulator {
	if audioRate == 0 {
		audioRate = 48000
	}
	return &SSBDemodulator{
		AudioRate:      audioRate,
		Sideband:       sideband,
		VoiceBandwidth: defaultVoiceBandwidthHz,
	}
}

// NormalizeSideband maps a sideband string to "upper" or "lower"
// ("" when unrecognized).
func NormalizeSideband(s string) string {
	switch NormalizeModulation(s) {
	case "UPPER", "USB":
		return "upper"
	case "LOWER", "LSB":
		return "lower"
	}
	return ""
}

// Name returns the modulation type ("USB" or "LSB").
func (s *SSBDemodulator) Name() string {
	if NormalizeSideband(s.Sideband) == "upper" {
		return "USB"
	}
	return "LSB"
}

// CanHandle reports if this demodulator handles the modulation string.
func (s *SSBDemodulator) CanHandle(modulation string) bool {
	switch NormalizeModulation(modulation) {
	case "SSB", "USB", "LSB":
		return true
	}
	return false
}

// CanHandlePair reports whether this demodulator handles the
// (modulation, subType) pair.
func (s *SSBDemodulator) CanHandlePair(modulation, subType string) bool {
	upper := NormalizeSideband(s.Sideband) == "upper"
	switch NormalizeModulation(modulation) {
	case "USB":
		return upper
	case "LSB":
		return !upper
	case "SSB":
		switch NormalizeModulation(subType) {
		case "":
			return true
		case "USB":
			return upper
		case "LSB":
			return !upper
		}
		return false
	}
	return false
}

// AudioSampleRate returns the output audio sample rate.
func (s *SSBDemodulator) AudioSampleRate() uint32 {
	return s.AudioRate
}

// Demodulate converts IQ samples to audio via frequency-domain
// sideband selection: forward FFT, keep only the wanted sideband
// (positive offsets for USB, negative for LSB) within
// VoiceBandwidth, restore Hermitian symmetry, inverse FFT, and take
// the real part — for SSB the selected sideband *is* the audio
// spectrum. Rejected-sideband energy is discarded, so USB and LSB
// are mutually exclusive (unlike the old AM-envelope path).
func (s *SSBDemodulator) Demodulate(iq []complex64, sampleRate uint32) ([]float32, error) {
	if len(iq) < 4 {
		return nil, fmt.Errorf("audio/ssb: not enough samples (%d)", len(iq))
	}
	if sampleRate == 0 {
		return nil, fmt.Errorf("audio/ssb: sample rate must be > 0")
	}

	upper := NormalizeSideband(s.Sideband) == "upper"
	bw := s.VoiceBandwidth
	if bw <= 0 {
		bw = defaultVoiceBandwidthHz
	}

	audio := ssbSelectBand(iq, sampleRate, upper, bw)

	// DC removal
	dc := 0.0
	for _, a := range audio {
		dc += a
	}
	dc /= float64(len(audio))
	for i := range audio {
		audio[i] -= dc
	}

	// Peak normalize
	maxVal := 0.0
	for _, a := range audio {
		if v := math.Abs(a); v > maxVal {
			maxVal = v
		}
	}
	if maxVal > 0 {
		for i := range audio {
			audio[i] /= maxVal
		}
	}

	// Decimate to audio rate
	decimateFactor := int(float64(sampleRate) / float64(s.AudioRate))
	if decimateFactor < 1 {
		decimateFactor = 1
	}

	// Moving-average low-pass before decimation (same contract as AM/FM)
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

	outAudio := make([]float32, 0, len(filtered)/decimateFactor)
	for i := 0; i < len(filtered); i += decimateFactor {
		v := float32(filtered[i])
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		outAudio = append(outAudio, v)
	}

	return outAudio, nil
}

// ssbSelectBand isolates the wanted sideband of zero-IF complex
// baseband and inverts it to a real, un-normalized audio signal at
// the input sample rate. Bin k of the forward transform holds
// frequency +k*df; bin nfft-k holds -k*df. Keeping bins 1..keepBins
// of the wanted sideband and mirroring them with conjugate symmetry
// makes the inverse transform real; DC and all out-of-band bins stay
// zero. Separated from Demodulate so tests can assert sideband
// rejection before peak normalization rescales any residual.
func ssbSelectBand(iq []complex64, sampleRate uint32, upper bool, bw float64) []float64 {
	n := len(iq)

	// Zero-pad to a power of two for the FFT.
	nfft := 1
	for nfft < n {
		nfft <<= 1
	}

	buf := make([]complex128, nfft)
	for i := 0; i < n; i++ {
		buf[i] = complex128(iq[i])
	}

	fft := fourier.NewCmplxFFT(nfft)
	x := fft.Coefficients(nil, buf)

	df := float64(sampleRate) / float64(nfft)
	keepBins := int(bw / df)
	if maxBins := nfft/2 - 1; keepBins > maxBins {
		keepBins = maxBins
	}
	if keepBins < 1 {
		keepBins = 1
	}

	out := make([]complex128, nfft)
	for k := 1; k <= keepBins; k++ {
		if upper {
			out[k] = x[k]
			out[nfft-k] = cmplx.Conj(x[k])
		} else {
			out[k] = cmplx.Conj(x[nfft-k])
			out[nfft-k] = x[nfft-k]
		}
	}

	samples := fft.Sequence(nil, out)

	audio := make([]float64, n)
	for i := 0; i < n; i++ {
		audio[i] = real(samples[i])
	}
	return audio
}