// Package audio provides signal demodulation and audio encoding.
package audio

// Demodulator converts IQ samples to audio.
// Implementations: FMDemodulator, AMDemodulator (and future modes).
type Demodulator interface {
	// Name returns the modulation type identifier (e.g. "WFM", "NFM", "AM", "SSB").
	Name() string

	// Demodulate converts complex IQ samples to mono audio (float32, -1.0 to 1.0).
	// iq is interleaved: I[0], Q[0], I[1], Q[1], ...
	// sampleRate is the IQ sample rate in Hz.
	// Returns audio samples at AudioSampleRate().
	Demodulate(iq []complex64, sampleRate uint32) ([]float32, error)

	// AudioSampleRate returns the output audio sample rate in Hz.
	AudioSampleRate() uint32

	// CanHandle reports whether this demodulator can process the given modulation string.
	CanHandle(modulation string) bool
}