// Max-pool decimation for the §18 spectrum tap.

package dsp

import "math"

// MaxPoolWrapped decimates a wrapped two-sided power spectrum (D4,
// §5.3 — as returned by ComputeIQFFT) to groups frequency-ordered bins
// via max-pooling (§18.1): group i reports the maximum of its member
// bins, so narrow tones survive decimation where average-pooling would
// erase exactly the peaks a spectrum display exists to show.
//
// Output index 0 is the lowest-frequency bin of the span (−fs/2,
// §18.2): the wrapped input is treated as fftshift-ordered, i.e.
// shifted index k reads wrapped index (k + n/2) % n. The DC bin
// therefore lands in the middle output group.
//
// The input slice is not modified. Returns nil when groups is not a
// positive divisor of len(powerDB).
func MaxPoolWrapped(powerDB []float64, groups int) []float64 {
	n := len(powerDB)
	if n == 0 || groups <= 0 || n%groups != 0 {
		return nil
	}
	half := n / 2
	g := n / groups
	out := make([]float64, groups)
	for j := 0; j < groups; j++ {
		max := math.Inf(-1)
		for k := j * g; k < (j+1)*g; k++ {
			if v := powerDB[(k+half)%n]; v > max {
				max = v
			}
		}
		out[j] = max
	}
	return out
}
