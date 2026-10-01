package dsp

import "math"

// log10impl delegates to math.Log10.
func log10impl(x float64) float64 {
	return math.Log10(x)
}