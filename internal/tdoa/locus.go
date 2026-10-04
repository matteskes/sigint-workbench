package tdoa

import "math"

// solveLocus publishes the 2-receiver locus (§9.5 degenerate solve):
// the hyperbola branch implied by the sign of τᵢⱼ, clipped to a
// locus_radius_m circle about the baseline midpoint (default 3× the
// baseline, §9.5 endpoint convention). No fix is produced.
func solveLocus(receivers []Receiver, o PairObs, ref int,
	cfg Config) SolveResult {
	br, ok := hyperbolaBranch(receivers, o, ref, cfg.LocusRadiusM)
	if !ok {
		return SolveResult{Reason: "coincident receivers"}
	}
	e1 := br.at(-br.sMax)
	e2 := br.at(br.sMax)
	return SolveResult{Locus: &[2]LatLng{
		fromENU(receivers[ref], e1[0], e1[1]),
		fromENU(receivers[ref], e2[0], e2[1]),
	}}
}

// LocusPoints samples the clipped branch for drawing (§9.6: the
// frontend draws the locus) — n points from one clipped endpoint to
// the other. n ≤ 1 yields just the endpoints' midpoint on the branch.
func LocusPoints(receivers []Receiver, o PairObs, radius float64,
	n int) []LatLng {
	if len(receivers) < 2 {
		return nil
	}
	br, ok := hyperbolaBranch(receivers, o, referenceIdx(receivers), radius)
	if !ok {
		return nil
	}
	if n < 2 {
		p := br.at(0)
		return []LatLng{fromENU(br.origin, p[0], p[1])}
	}
	pts := make([]LatLng, n)
	for i := range pts {
		s := -br.sMax + 2*br.sMax*float64(i)/float64(n-1)
		p := br.at(s)
		pts[i] = fromENU(br.origin, p[0], p[1])
	}
	return pts
}

// branch is one hyperbola branch in the reference receiver's ENU
// frame, canonically x²/a² − y²/b² = 1 with x = sign(τᵢⱼ)·a·cosh u,
// y = b·sinh u along the baseline, clipped to s = sinh u ≤ sMax by
// the locus-radius circle (§9.5 endpoint convention).
type branch struct {
	origin     Receiver
	mid, u, v  [2]float64
	a, b, sign float64
	sMax       float64
	baselineM  float64
}

// hyperbolaBranch builds the branch for one pair. |c·τᵢⱼ| exceeding
// the baseline is geometrically impossible and clamped; a locus
// radius below the branch vertex falls back to a parameter cap.
func hyperbolaBranch(receivers []Receiver, o PairObs, ref int,
	radius float64) (branch, bool) {
	pi := toENU(receivers[ref], receivers[o.I])
	pj := toENU(receivers[ref], receivers[o.J])
	d := dist(pi, pj)
	if d == 0 {
		return branch{}, false
	}
	mid := [2]float64{(pi[0] + pj[0]) / 2, (pi[1] + pj[1]) / 2}
	f := d / 2 // focal half-distance
	a := math.Abs(o.TauNS) * 1e-9 * SpeedOfLight / 2
	if a >= f {
		a = f * 0.999
	}
	b := math.Sqrt(f*f - a*a)
	if radius <= 0 {
		radius = 3 * d // §9.5 default: 3× the pair's baseline
	}
	sMax := 1.0
	if radius > a {
		if den := a*a + b*b; den > 0 {
			sMax = math.Sqrt((radius*radius - a*a) / den)
		}
	}
	u := [2]float64{(pj[0] - pi[0]) / d, (pj[1] - pi[1]) / d}
	sign := 1.0
	if o.TauNS < 0 {
		sign = -1
	}
	return branch{
		origin:    receivers[ref],
		mid:       mid,
		u:         u,
		v:         [2]float64{-u[1], u[0]},
		a:         a,
		b:         b,
		sign:      sign,
		sMax:      sMax,
		baselineM: d,
	}, true
}

// at evaluates the branch at s = sinh u.
func (br branch) at(s float64) [2]float64 {
	x := br.sign * br.a * math.Sqrt(1+s*s)
	y := br.b * s
	return [2]float64{
		br.mid[0] + br.u[0]*x + br.v[0]*y,
		br.mid[1] + br.u[1]*x + br.v[1]*y,
	}
}

// referenceIdx picks the reference receiver (§9.5): best clock sync
// quality first, then centrality — single-host networks have uniform
// clocks, so centrality decides there.
func referenceIdx(rs []Receiver) int {
	best := 0
	bestSum := math.Inf(1)
	for i := range rs {
		s := 0.0
		for j := range rs {
			if i == j {
				continue
			}
			p := toENU(rs[i], rs[j])
			s += math.Hypot(p[0], p[1])
		}
		better := rs[i].ClockUncertaintyNS < rs[best].ClockUncertaintyNS ||
			(rs[i].ClockUncertaintyNS == rs[best].ClockUncertaintyNS &&
				s < bestSum)
		if better {
			best, bestSum = i, s
		}
	}
	return best
}

// ChoosePairs implements the §9.5 pair-selection policy: all receiver
// pairs while C(N,2) ≤ cap, else a reference-receiver star — pairs
// kept low, every receiver still on ≥ 1 baseline.
func ChoosePairs(rs []Receiver, cap int) [][2]int {
	if cap <= 0 {
		cap = 15
	}
	var all [][2]int
	for i := range rs {
		for j := i + 1; j < len(rs); j++ {
			all = append(all, [2]int{i, j})
		}
	}
	if len(all) <= cap {
		return all
	}
	ref := referenceIdx(rs)
	var star [][2]int
	for i := range rs {
		if i != ref {
			star = append(star, [2]int{ref, i})
		}
	}
	return star
}

// toENU projects (lat, lng) into the east/north tangent plane at the
// origin receiver (§9.5 projection).
func toENU(origin Receiver, r Receiver) [2]float64 {
	dLat := (r.Lat - origin.Lat) * math.Pi / 180
	dLng := (r.Lng - origin.Lng) * math.Pi / 180
	phi0 := origin.Lat * math.Pi / 180
	return [2]float64{
		earthRadiusMean * dLng * math.Cos(phi0),
		earthRadiusMean * dLat,
	}
}

// fromENU inverts toENU.
func fromENU(origin Receiver, e, n float64) LatLng {
	lat := origin.Lat + n/earthRadiusMean*180/math.Pi
	cosPhi0 := math.Cos(origin.Lat * math.Pi / 180)
	lng := origin.Lng
	if cosPhi0 != 0 {
		lng = origin.Lng + e/(earthRadiusMean*cosPhi0)*180/math.Pi
	}
	return LatLng{Lat: lat, Lng: lng}
}
