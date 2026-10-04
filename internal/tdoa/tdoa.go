// Package tdoa locates emitters from time-difference-of-arrival
// measurements (§9.5). It is a pure library: no service, database, or
// wire-format imports. The engine has three parts — Buffer (per-
// receiver gap-free run store and window alignment), Correlator
// (GCC-PHAT pair delays with parabolic refinement), and Solver
// (weighted Gauss–Newton with outlier rejection) — exercised directly
// by the simulator tests with injected delays (§9.6).
package tdoa

import (
	"time"
)

// SpeedOfLight is the vacuum speed of light in m/s (§9.5: c ≈ 300 m/µs).
const SpeedOfLight = 299792458.0

// earthRadiusMean is the WGS84 mean-sphere radius behind the ENU
// tangent-plane projection (§9.5): good to ~0.3% at the ≤ 50 km
// scales a single-host network works at.
const earthRadiusMean = 6371008.8

// Receiver describes one participating receiver. Lat/Lng come from
// the sdr row (§12.1); ClockUncertaintyNS is the §16 sync quality in
// nanoseconds — 0 for single-host solves, where every dongle shares
// one clock by construction (§9.5).
type Receiver struct {
	ID                 string
	Lat, Lng           float64
	ClockUncertaintyNS float64
}

// Run is one receiver's contiguous, gap-free sample run (§9.5
// alignment buffer). Samples are band-shifted complex baseband at
// SampleRate; AnchorUTC is the CLOCK_REALTIME timestamp of
// Samples[0] (§4.5 v2 header time) and StartSample that sample's
// sample_index.
type Run struct {
	ReceiverID  string
	AnchorUTC   time.Time
	StartSample uint64
	SampleRate  float64 // Hz
	Samples     []complex128
}

// EndTime returns the anchor time of one-past-the-end sample.
func (r Run) EndTime() time.Time {
	return r.AnchorUTC.Add(time.Duration(float64(time.Second) *
		float64(len(r.Samples)) / r.SampleRate))
}

// Slice trims the run to [t0, t1), re-anchoring at t0. It reports
// false unless the run fully covers the interval.
func (r Run) Slice(t0, t1 time.Time) (Run, bool) {
	if r.SampleRate <= 0 || t0.Before(r.AnchorUTC) || !t1.After(t0) ||
		t1.After(r.EndTime()) {
		return Run{}, false
	}
	i := int(float64(t0.Sub(r.AnchorUTC)) / float64(time.Second) * r.SampleRate)
	n := int(float64(t1.Sub(t0)) / float64(time.Second) * r.SampleRate)
	if i < 0 || i+n > len(r.Samples) {
		return Run{}, false
	}
	return Run{
		ReceiverID:  r.ReceiverID,
		AnchorUTC:   t0,
		StartSample: r.StartSample + uint64(i),
		SampleRate:  r.SampleRate,
		Samples:     r.Samples[i : i+n],
	}, true
}

// Window is one receiver's aligned contribution to a solve: the same
// wall-clock span as every other window in the attempt, at the common
// solve rate (§9.5 step 1).
type Window struct {
	ReceiverID string
	SampleRate float64
	Samples    []complex128
}

// PairDelay is one pair's measured delay: TauNS = τᵢⱼ = τᵢ − τⱼ =
// (dᵢ − dⱼ)/c (§9.5 hyperbola), positive when the emitter is farther
// from receiver i than from receiver j. Peak is the normalized
// correlation peak prominence in [0, 1].
type PairDelay struct {
	TauNS float64
	Peak  float64
}

// PairObs is a solver-ready measurement over receiver indices.
// SigmaNS is the combined 1σ time uncertainty (peak + clock terms,
// §9.5 weighting); single-host solves pass a small uniform σ.
type PairObs struct {
	I, J    int
	TauNS   float64
	SigmaNS float64
}

// Fix is a point solution (§9.5 step 4).
type Fix struct {
	Lat, Lng     float64
	ResidualNS   float64 // post-solve RMS time residual
	PairsUsed    int
	MaxBaselineM float64
	Reference    string
	CovPosDef    bool // 2×2 ENU covariance positive definite
}

// LatLng is a geographic point.
type LatLng struct{ Lat, Lng float64 }

// SolveResult reports one solve attempt. Exactly one of Fix/Locus is
// non-nil on a geometry success — 2-receiver solves are locus-only,
// §9.5 — and Accepted applies to point fixes (residual within
// budget + covariance gate, §9.6). Rejected holds observation indices
// dropped by the outlier loop. Reason is set whenever no geometry
// was produced or the fix missed the accuracy budget.
type SolveResult struct {
	Fix      *Fix
	Locus    *[2]LatLng
	Accepted bool
	Rejected []int
	Reason   string
}

// Config carries the §9.6 engine knobs a solve needs. Zero fields
// take the documented defaults.
type Config struct {
	// AccuracyBudgetM is the post-solve RMS residual gate — 50 m
	// simulator, 200 m on-air (§9.6). It also scales the §9.5
	// clock-uncertainty eligibility gate.
	AccuracyBudgetM float64
	// OutlierSigma is the normalized-residual rejection threshold
	// (default 3, §9.5).
	OutlierSigma float64
	// LocusRadiusM clips 2-receiver loci (§9.5 endpoint
	// convention); 0 ⇒ 3× the pair's baseline.
	LocusRadiusM float64
	// PairCap bounds all-pairs correlation (default 15, §9.6) —
	// applied by ChoosePairs, which Solve assumes was consulted.
	PairCap int
	// MaxIterations bounds Gauss–Newton; 0 ⇒ 50.
	MaxIterations int
}

// withDefaults fills zero-valued fields (§9.6 defaults).
func (c Config) withDefaults() Config {
	if c.AccuracyBudgetM <= 0 {
		c.AccuracyBudgetM = 200
	}
	if c.OutlierSigma <= 0 {
		c.OutlierSigma = 3
	}
	if c.PairCap <= 0 {
		c.PairCap = 15
	}
	if c.MaxIterations <= 0 {
		c.MaxIterations = 50
	}
	return c
}
