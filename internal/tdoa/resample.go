package tdoa

import "math"

// Resample converts complex baseband between sample rates with
// 4-point cubic Lagrange interpolation (§9.5 mixed-rate
// prerequisite). Output sample k lands at input position k·fsIn/fsOut
// — phase-true to well within a sample for the bandlimited baseband
// the TDOA path sees; edge effects are confined to the first and
// last two input samples.
func Resample(in []complex128, fsIn, fsOut float64) []complex128 {
	if len(in) == 0 || fsIn <= 0 || fsOut <= 0 {
		return nil
	}
	ratio := fsIn / fsOut
	n := int(math.Round(float64(len(in)) / ratio))
	out := make([]complex128, n)
	for k := range out {
		pos := float64(k) * ratio
		i := int(pos)
		out[k] = cubicAt(in, i, pos-float64(i))
	}
	return out
}

// cubicAt evaluates a 4-point cubic Lagrange interpolant at i+t with
// t ∈ [0, 1), clamped at the slice edges.
func cubicAt(s []complex128, i int, t float64) complex128 {
	at := func(j int) complex128 {
		if j < 0 {
			j = 0
		}
		if j >= len(s) {
			j = len(s) - 1
		}
		return s[j]
	}
	p, q, r, u := at(i-1), at(i), at(i+1), at(i+2)
	a := -0.5*p + 1.5*q - 1.5*r + 0.5*u
	b := p - 2.5*q + 2*r - 0.5*u
	c := -0.5*p + 0.5*r
	ct := complex(t, 0)
	return ((a*ct+b)*ct+c)*ct + q
}
