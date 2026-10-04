package tdoa

import (
	"math"
	"testing"
)

// fixedNetwork is a hand-placed 5-receiver ring around the sim
// origin with an emitter inside it.
func fixedNetwork() ([]Receiver, [2]float64) {
	enu := [][2]float64{
		{-4000, -2500}, {4500, -2000}, {3800, 4200},
		{-3500, 4000}, {200, -5200},
	}
	tx := [2]float64{900, 1100}
	recs := make([]Receiver, len(enu))
	for i, p := range enu {
		ll := fromENU(simOrigin, p[0], p[1])
		recs[i] = Receiver{
			ID:  "rx" + string(rune('0'+i)),
			Lat: ll.Lat, Lng: ll.Lng,
		}
	}
	return recs, tx
}

// exactObs builds geometry-true pair delays from the emitter. All
// positions are projected through the SAME origin the solver will
// use (the reference receiver) — spherical ENU is not exactly
// translational across origins, so cross-frame distances would leak
// meters of inconsistency into "exact" observations.
func exactObs(recs []Receiver, tx [2]float64) []PairObs {
	ref := referenceIdx(recs)
	txLL := fromENU(simOrigin, tx[0], tx[1])
	txRef := toENU(recs[ref], Receiver{Lat: txLL.Lat, Lng: txLL.Lng})
	pts := make([][2]float64, len(recs))
	for i := range recs {
		pts[i] = toENU(recs[ref], recs[i])
	}
	var obs []PairObs
	for i := range recs {
		for j := i + 1; j < len(recs); j++ {
			di := math.Hypot(txRef[0]-pts[i][0], txRef[1]-pts[i][1])
			dj := math.Hypot(txRef[0]-pts[j][0], txRef[1]-pts[j][1])
			obs = append(obs, PairObs{
				I: i, J: j,
				TauNS:   (di - dj) / SpeedOfLight * 1e9,
				SigmaNS: 1,
			})
		}
	}
	return obs
}

func fixError(t *testing.T, res SolveResult, tx [2]float64) float64 {
	t.Helper()
	if res.Fix == nil {
		t.Fatalf("no fix: %s", res.Reason)
	}
	p := toENU(simOrigin, Receiver{Lat: res.Fix.Lat, Lng: res.Fix.Lng})
	return math.Hypot(p[0]-tx[0], p[1]-tx[1])
}

func TestSolverExactObservations(t *testing.T) {
	recs, tx := fixedNetwork()
	res := Solve(recs, exactObs(recs, tx), Config{AccuracyBudgetM: 50})
	if !res.Accepted {
		t.Fatalf("not accepted: %s", res.Reason)
	}
	if e := fixError(t, res, tx); e > 0.01 {
		t.Fatalf("fix error = %.4f m, want ≈ 0", e)
	}
	if res.Fix.PairsUsed != 10 {
		t.Fatalf("pairs used = %d, want 10", res.Fix.PairsUsed)
	}
	if len(res.Rejected) != 0 || !res.Fix.CovPosDef {
		t.Fatalf("rejected = %v, covPosDef = %v",
			res.Rejected, res.Fix.CovPosDef)
	}
}

// TestSolverRejectsSingleCorruptedPair is the solver-level §9.5
// obligation: corrupting exactly one pair's measurement drops that
// pair alone and leaves the fix intact.
func TestSolverRejectsSingleCorruptedPair(t *testing.T) {
	recs, tx := fixedNetwork()
	obs := exactObs(recs, tx)
	obs[2].TauNS += 10000 // 3 km pair bias, ~10⁴σ at σ = 1 ns
	res := Solve(recs, obs, Config{AccuracyBudgetM: 50})
	if !res.Accepted {
		t.Fatalf("not accepted: %s", res.Reason)
	}
	if len(res.Rejected) != 1 || res.Rejected[0] != 2 {
		t.Fatalf("rejected = %v, want [2]", res.Rejected)
	}
	if e := fixError(t, res, tx); e > 0.01 {
		t.Fatalf("fix error after rejection = %.4f m, want ≈ 0", e)
	}
}

// TestLocusBranch pins the 2-receiver path: locus-only (no fix), the
// branch carries the emitter, and the endpoints respect the locus
// radius (§9.5 endpoint convention).
func TestLocusBranch(t *testing.T) {
	recs, tx := fixedNetwork()
	two := recs[:2]
	pts := make([][2]float64, 2)
	for i := range two {
		pts[i] = toENU(simOrigin, two[i])
	}
	di := math.Hypot(tx[0]-pts[0][0], tx[1]-pts[0][1])
	dj := math.Hypot(tx[0]-pts[1][0], tx[1]-pts[1][1])
	tau := (di - dj) / SpeedOfLight * 1e9
	res := Solve(two, []PairObs{{I: 0, J: 1, TauNS: tau, SigmaNS: 1}},
		Config{AccuracyBudgetM: 50})
	if res.Locus == nil || res.Fix != nil {
		t.Fatalf("2-receiver solve must be locus-only, got fix=%v locus=%v",
			res.Fix, res.Locus)
	}
	sampled := LocusPoints(two, PairObs{I: 0, J: 1, TauNS: tau}, 0, 40000)
	best := math.Inf(1)
	for _, p := range sampled {
		e := toENU(simOrigin, Receiver{Lat: p.Lat, Lng: p.Lng})
		best = math.Min(best, math.Hypot(e[0]-tx[0], e[1]-tx[1]))
	}
	if best > 5 {
		t.Fatalf("nearest branch point to emitter = %.1f m, want < 5", best)
	}
	// Endpoints sit on the clip circle — measured in the solver's own
	// frame (cross-frame projection at 25 km radii is off by tens of
	// meters, d²/2R tangent-plane error).
	ref := referenceIdx(two)
	ptsRef := make([][2]float64, 2)
	for i := range two {
		ptsRef[i] = toENU(two[ref], two[i])
	}
	midRef := [2]float64{
		(ptsRef[0][0] + ptsRef[1][0]) / 2,
		(ptsRef[0][1] + ptsRef[1][1]) / 2,
	}
	base := math.Hypot(ptsRef[0][0]-ptsRef[1][0], ptsRef[0][1]-ptsRef[1][1])
	for _, e := range *res.Locus {
		ep := toENU(two[ref], Receiver{Lat: e.Lat, Lng: e.Lng})
		d := math.Hypot(ep[0]-midRef[0], ep[1]-midRef[1])
		if d > 3*base+1.0 {
			t.Fatalf("endpoint %.1f m from midpoint > radius %.1f m",
				d, 3*base)
		}
		if d < 3*base-10.0 {
			t.Fatalf("endpoint %.1f m from midpoint, branch not clipped to %.1f m",
				d, 3*base)
		}
	}
}

func TestChoosePairs(t *testing.T) {
	recs, _ := fixedNetwork()
	if got := ChoosePairs(recs, 15); len(got) != 10 {
		t.Fatalf("all-pairs = %d, want 10", len(got))
	}
	six := append(append([]Receiver{}, recs...),
		Receiver{ID: "rx5", Lat: recs[0].Lat + 0.01, Lng: recs[0].Lng})
	star := ChoosePairs(six, 7)
	if len(star) != 5 {
		t.Fatalf("star = %d pairs, want 5", len(star))
	}
	ref := referenceIdx(six)
	for _, p := range star {
		if p[0] != ref && p[1] != ref {
			t.Fatalf("star pair %v not on reference %d", p, ref)
		}
	}
}
