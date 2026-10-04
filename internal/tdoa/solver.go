package tdoa

import (
	"fmt"
	"math"
)

// Solve computes one fix or locus from pair observations (§9.5
// steps 3–4). obs reference receiver indices; callers pre-select
// pairs with ChoosePairs (the pair_cap policy). The pipeline is the
// §9.5 one: clock-uncertainty eligibility gate, 1/σ² weighting,
// damped Gauss–Newton from a centroid seed, outlier-rejection loop
// while ≥ 2 independent baselines remain, then the positive-definite
// covariance gate and the residual/accuracy-budget mapping.
func Solve(receivers []Receiver, obs []PairObs, cfg Config) SolveResult {
	cfg = cfg.withDefaults()
	if len(receivers) < 2 || len(obs) == 0 {
		return SolveResult{Reason: "need at least 2 receivers and 1 pair"}
	}
	ref := referenceIdx(receivers)
	if len(receivers) == 2 {
		return solveLocus(receivers, obs[0], ref, cfg)
	}

	pts := make([][2]float64, len(receivers))
	for i := range receivers {
		pts[i] = toENU(receivers[ref], receivers[i])
	}

	// Eligibility: combined clock uncertainty, scaled by c, inside
	// the accuracy budget (§9.5).
	budgetNS := cfg.AccuracyBudgetM / SpeedOfLight * 1e9
	elig := make([]PairObs, 0, len(obs))
	eligIdx := make([]int, 0, len(obs))
	for i, o := range obs {
		if o.I < 0 || o.J < 0 || o.I == o.J ||
			o.I >= len(receivers) || o.J >= len(receivers) ||
			o.SigmaNS <= 0 {
			continue
		}
		clockNS := math.Hypot(receivers[o.I].ClockUncertaintyNS,
			receivers[o.J].ClockUncertaintyNS)
		if clockNS > budgetNS {
			continue
		}
		elig = append(elig, o)
		eligIdx = append(eligIdx, i)
	}
	if len(elig) == 0 {
		return SolveResult{Reason: "no eligible pairs"}
	}

	active := make([]int, len(elig))
	for i := range active {
		active[i] = i
	}
	var rejected []int
	x := centroid(pts)
	var res []float64
	for {
		x, res, _ = gaussNewton(pts, elig, active, x, cfg.MaxIterations)
		worstRank, worstN := -1, 0.0
		for rank, oi := range active {
			sigmaM := elig[oi].SigmaNS * 1e-9 * SpeedOfLight
			if n := math.Abs(res[rank]) / sigmaM; n > worstN {
				worstN, worstRank = n, rank
			}
		}
		// Reject only while ≥ 2 independent baselines remain
		// (§9.5): with k active pairs there are k−1 independent.
		if worstRank < 0 || worstN <= cfg.OutlierSigma ||
			len(active)-1 < 2 {
			break
		}
		rejected = append(rejected, eligIdx[active[worstRank]])
		active = append(active[:worstRank], active[worstRank+1:]...)
	}

	// Anchor-offset hypothesis screen (§9.5 corruption model): when
	// one receiver's anchor is wrong, every pair it appears in is
	// biased coherently and the drop-worst loop above can adopt the
	// biased subset — a low-residual but wrong fix. Hypothesize each
	// receiver as the biased one (fit only the pairs NOT touching
	// it) and keep whichever fit — these or the loop's — claims the
	// σ-inlier majority over all eligible pairs. Ties keep the
	// loop's fit.
	resAt := func(x [2]float64, o PairObs) float64 {
		return dist(x, pts[o.I]) - dist(x, pts[o.J]) -
			o.TauNS*1e-9*SpeedOfLight
	}
	inliers := func(x [2]float64) int {
		n := 0
		for _, o := range elig {
			if math.Abs(resAt(x, o)) <=
				cfg.OutlierSigma*o.SigmaNS*1e-9*SpeedOfLight {
				n++
			}
		}
		return n
	}
	best := inliers(x)
	for r := range receivers {
		var sub []int
		for i, o := range elig {
			if o.I != r && o.J != r {
				sub = append(sub, i)
			}
		}
		if len(sub)-2 < 2 { // need ≥ 2 independent baselines
			continue
		}
		xh, _, ok := gaussNewton(pts, elig, sub, centroid(pts),
			cfg.MaxIterations)
		if !ok {
			continue
		}
		if _, pd := covariance(pts, elig, sub, xh, 1); !pd {
			continue
		}
		if n := inliers(xh); n > best {
			best = n
			x = xh
		}
	}
	// Classify every eligible pair at the winning fit.
	active = active[:0]
	rejected = rejected[:0]
	for i, o := range elig {
		if math.Abs(resAt(x, o)) <=
			cfg.OutlierSigma*o.SigmaNS*1e-9*SpeedOfLight {
			active = append(active, i)
		} else {
			rejected = append(rejected, eligIdx[i])
		}
	}
	res = evalResiduals(pts, elig, active, x)

	rmsNS, rmsM := rmsResidual(res)
	_, pd := covariance(pts, elig, active, x, 1)
	maxBase := 0.0
	for _, oi := range active {
		if d := dist(pts[elig[oi].I], pts[elig[oi].J]); d > maxBase {
			maxBase = d
		}
	}
	pos := fromENU(receivers[ref], x[0], x[1])
	out := SolveResult{Rejected: rejected, Fix: &Fix{
		Lat:          pos.Lat,
		Lng:          pos.Lng,
		ResidualNS:   rmsNS,
		PairsUsed:    len(active),
		MaxBaselineM: maxBase,
		Reference:    receivers[ref].ID,
		CovPosDef:    pd,
	}}
	switch {
	case rmsM > cfg.AccuracyBudgetM:
		out.Reason = fmt.Sprintf("rms residual %.1f m over budget %.1f m",
			rmsM, cfg.AccuracyBudgetM)
	case !pd:
		out.Reason = "covariance not positive definite"
	default:
		out.Accepted = true
	}
	return out
}

// gaussNewton runs damped Gauss–Newton (§9.5) from x and returns the
// refined position and per-active-pair residuals in meters. Weights
// are plain 1/σ²: robust inner losses distort cold-start weighting
// (residuals are km-scale at the seed) and let a coherent biased
// subset win the fit — outlier handling lives in Solve's rejection
// loop and hypothesis screen instead.
func gaussNewton(pts [][2]float64, obs []PairObs, active []int,
	x [2]float64, maxIt int) ([2]float64, []float64, bool) {
	res := evalResiduals(pts, obs, active, x)
	for it := 0; it < maxIt; it++ {
		var g [2]float64
		var h [2][2]float64
		for rank, oi := range active {
			o := obs[oi]
			sigmaM := o.SigmaNS * 1e-9 * SpeedOfLight
			w := 1 / (sigmaM * sigmaM)
			j := jacobianRow(x, pts[o.I], pts[o.J])
			g[0] += w * res[rank] * j[0]
			g[1] += w * res[rank] * j[1]
			h[0][0] += w * j[0] * j[0]
			h[0][1] += w * j[0] * j[1]
			h[1][0] = h[0][1]
			h[1][1] += w * j[1] * j[1]
		}
		// Tikhonov damping keeps the 2×2 well conditioned on the
		// baselines' extension (§9.5 "damped").
		h[0][0] += 1e-9
		h[1][1] += 1e-9
		det := h[0][0]*h[1][1] - h[0][1]*h[1][0]
		if det == 0 {
			return x, res, false
		}
		dx := [2]float64{
			(h[1][1]*g[0] - h[0][1]*g[1]) / det,
			(h[0][0]*g[1] - h[1][0]*g[0]) / det,
		}
		x[0] -= dx[0]
		x[1] -= dx[1]
		res = evalResiduals(pts, obs, active, x)
		if math.Hypot(dx[0], dx[1]) < 1e-6 {
			break
		}
	}
	return x, res, true
}

// evalResiduals returns per-active-pair hyperbola residuals in
// meters: (‖x−rᵢ‖ − ‖x−rⱼ‖) − c·τᵢⱼ (§9.5 hyperbola).
func evalResiduals(pts [][2]float64, obs []PairObs, active []int,
	x [2]float64) []float64 {
	res := make([]float64, len(active))
	for rank, oi := range active {
		o := obs[oi]
		res[rank] = dist(x, pts[o.I]) - dist(x, pts[o.J]) -
			o.TauNS*1e-9*SpeedOfLight
	}
	return res
}

// jacobianRow is ∂/∂x of (‖x−rᵢ‖ − ‖x−rⱼ‖) = uᵢ − uⱼ with unit
// vectors uₖ = (x−rₖ)/‖x−rₖ‖.
func jacobianRow(x, ri, rj [2]float64) [2]float64 {
	di, dj := dist(x, ri), dist(x, rj)
	if di == 0 || dj == 0 {
		return [2]float64{}
	}
	return [2]float64{
		(x[0]-ri[0])/di - (x[0]-rj[0])/dj,
		(x[1]-ri[1])/di - (x[1]-rj[1])/dj,
	}
}

// covariance returns (JᵀWJ)⁻¹·scale — the ENU covariance behind the
// §9.6 overwrite gate — and whether it is positive definite.
func covariance(pts [][2]float64, obs []PairObs, active []int,
	x [2]float64, scale float64) ([2][2]float64, bool) {
	var h [2][2]float64
	for _, oi := range active {
		o := obs[oi]
		sigmaM := o.SigmaNS * 1e-9 * SpeedOfLight
		w := 1 / (sigmaM * sigmaM)
		j := jacobianRow(x, pts[o.I], pts[o.J])
		h[0][0] += w * j[0] * j[0]
		h[0][1] += w * j[0] * j[1]
		h[1][0] = h[0][1]
		h[1][1] += w * j[1] * j[1]
	}
	det := h[0][0]*h[1][1] - h[0][1]*h[1][0]
	var cov [2][2]float64
	if det == 0 || h[0][0] <= 0 {
		return cov, false
	}
	cov[0][0] = h[1][1] / det * scale
	cov[1][1] = h[0][0] / det * scale
	cov[0][1] = -h[0][1] / det * scale
	cov[1][0] = cov[0][1]
	return cov, det > 0
}

// dist is the Euclidean norm of a−b in the ENU plane.
func dist(a, b [2]float64) float64 {
	return math.Hypot(a[0]-b[0], a[1]-b[1])
}

// centroid is the Gauss–Newton seed (§9.5 seeds from §9.3 when the
// processor has a placement; the engine exposes the neutral default).
func centroid(pts [][2]float64) [2]float64 {
	var c [2]float64
	for _, p := range pts {
		c[0] += p[0]
		c[1] += p[1]
	}
	c[0] /= float64(len(pts))
	c[1] /= float64(len(pts))
	return c
}

// rmsResidual summarizes residuals in meters and nanoseconds.
func rmsResidual(res []float64) (rmsNS, rmsM float64) {
	if len(res) == 0 {
		return 0, 0
	}
	s := 0.0
	for _, r := range res {
		s += r * r
	}
	rmsM = math.Sqrt(s / float64(len(res)))
	return rmsM / SpeedOfLight * 1e9, rmsM
}
