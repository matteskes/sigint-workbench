package dsp

import (
	"fmt"
	"math"
)

// WindowKind selects the FFT window applied before ComputeIQFFT
// (§5.7). Rectangular is the shipped default: the ONNX model
// (models/train.py) was trained on unwindowed frames, so any other
// window is a train/infer mismatch until train.py mirrors it and the
// model is retrained. Windowing also changes the unnormalized power
// scale, so a window change forces a §6.2 recalibration as well.
type WindowKind int

const (
	WindowRectangular WindowKind = iota
	WindowHann
	WindowHamming
	WindowBlackman
)

// ParseWindow maps the config/signal-processor.yaml fft.window string
// to a WindowKind. Unknown names error — fail at startup, never fall
// back to rectangular silently.
func ParseWindow(name string) (WindowKind, error) {
	switch name {
	case "", "rectangular":
		return WindowRectangular, nil
	case "hann":
		return WindowHann, nil
	case "hamming":
		return WindowHamming, nil
	case "blackman":
		return WindowBlackman, nil
	default:
		return WindowRectangular, fmt.Errorf(
			"dsp: unknown fft.window %q (want rectangular|hann|hamming|blackman)",
			name)
	}
}

// ApplyWindow multiplies interleaved IQ samples in place by the
// periodic (DFT-even) window of the given kind: pair i (samples
// 2i, 2i+1) gets coefficient w[i]. Rectangular is a no-op, so the
// default config path leaves the spectrum byte-identical to the
// unwindowed pipeline the model was trained on.
func ApplyWindow(samples []float64, kind WindowKind) {
	if kind == WindowRectangular || len(samples) < 2 {
		return
	}
	n := len(samples) / 2 // IQ pairs; a trailing half-pair is ignored
	for i := 0; i < n; i++ {
		t := 2 * math.Pi * float64(i) / float64(n)
		var w float64
		switch kind {
		case WindowHann:
			w = 0.5 * (1 - math.Cos(t))
		case WindowHamming:
			w = 0.54 - 0.46*math.Cos(t)
		case WindowBlackman:
			w = 0.42 - 0.5*math.Cos(t) + 0.08*math.Cos(2*t)
		}
		samples[2*i] *= w
		samples[2*i+1] *= w
	}
}
