// Package dsp — power spectral density computation.
package dsp

// PSD computes the power spectral density from time-domain samples.
// Returns frequency bins (Hz) and PSD values (dB/Hz).
func PSD(samples []float64, sampleRate uint32) (freqs []float64, psd []float64) {
	result, err := ComputeFFT(samples, sampleRate)
	if err != nil || result == nil {
		return nil, nil
	}
	freqs = result.Frequencies
	psd = result.PowerDB
	return freqs, psd
}