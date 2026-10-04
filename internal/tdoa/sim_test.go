package tdoa

import (
	"fmt"
	"math"
	"math/cmplx"
	"math/rand"
	"testing"
	"time"
)

// The §9.5 simulator: geometry-true delays are synthesized
// analytically — each receiver's waveform is evaluated at (nT − τᵢ) —
// so sub-sample precision is exact by construction. The corruption
// model is one receiver's anchor offset, which its pairs inherit.

const (
	simFs      = 2.4e6 // primary rate, Hz
	simFsLow   = 1.8e6 // mixed-rate tier, Hz
	simWindowS = 10e-3 // tdoa.window_ms
	simSNRDB   = 20    // stated simulator gate SNR (§9.5)
	simSigmaNS = 5     // measurement σ handed to the solver (matches
	// the measured per-pair jitter at 20 dB; a laxer σ lets the
	// anchor bias hide below the 3σ outlier threshold)
	simAnchorNS = 500 // corrupted receiver's anchor offset (100σ)
)

var simOrigin = Receiver{ID: "origin", Lat: 52.52, Lng: 13.405}

type simTone struct {
	f, amp, phase float64
}

type simScenario struct {
	receivers []Receiver
	runs      map[string]Run
	tx        [2]float64 // emitter, ENU meters at simOrigin
	corruptID string     // receiver carrying the anchor offset
}

func genTones(rg *rand.Rand) []simTone {
	tones := make([]simTone, 0, 8)
	for k := 0; k < 8; k++ {
		tones = append(tones, simTone{
			f:     30e3 + 18e3*float64(k) + rg.Float64()*2e3,
			amp:   0.5 + rg.Float64()*0.5,
			phase: rg.Float64() * 2 * math.Pi,
		})
	}
	return tones
}

// simWaveform evaluates the windowed multitone at continuous time t
// (caller pre-subtracts τᵢ), raised-cosine gated over the window.
func simWaveform(t float64, tones []simTone) complex128 {
	const edge = 1e-3
	if t < 0 || t > simWindowS {
		return 0
	}
	env := 1.0
	switch {
	case t < edge:
		env = smooth01(t / edge)
	case t > simWindowS-edge:
		env = smooth01((simWindowS - t) / edge)
	}
	var v complex128
	for _, tn := range tones {
		v += complex(tn.amp, 0) *
			cmplx.Exp(complex(0, 2*math.Pi*tn.f*t+tn.phase))
	}
	return complex(env, 0) * v
}

func smooth01(x float64) float64 {
	x = math.Max(0, math.Min(1, x))
	return x * x * (3 - 2*x)
}

// genScenario builds a 5-receiver virtual network: receivers on an
// annulus with seeded jitter, emitter inside the ring, one receiver
// given an anchor offset. fsFor optionally varies per-receiver
// sample rates (§9.5 mixed-rate tier).
func genScenario(seed int64, fsFor func(i int) float64) simScenario {
	rg := rand.New(rand.NewSource(seed))
	sc := simScenario{runs: map[string]Run{}}
	for i := 0; i < 5; i++ {
		ang := float64(i)/5*2*math.Pi + (rg.Float64()-0.5)*0.4
		rad := 3000 + rg.Float64()*5000
		ll := fromENU(simOrigin, rad*math.Cos(ang), rad*math.Sin(ang))
		sc.receivers = append(sc.receivers, Receiver{
			ID:  fmt.Sprintf("rx%d", i),
			Lat: ll.Lat, Lng: ll.Lng,
		})
	}
	ta := rg.Float64() * 2 * math.Pi
	tr := rg.Float64() * 4000
	sc.tx = [2]float64{tr * math.Cos(ta), tr * math.Sin(ta)}
	tones := genTones(rg)
	corrupt := rg.Intn(5)
	sc.corruptID = sc.receivers[corrupt].ID
	t0 := time.Unix(1700000000, 0).UTC()
	// Synthesize delays in the SAME frame the solver will project
	// into (the reference receiver's) — spherical ENU is not
	// exactly translational across origins, and cross-frame delays
	// would leak a few ns of systematic error into every pair.
	ref := referenceIdx(sc.receivers)
	txLL := fromENU(simOrigin, sc.tx[0], sc.tx[1])
	txRef := toENU(sc.receivers[ref],
		Receiver{Lat: txLL.Lat, Lng: txLL.Lng})
	for i, r := range sc.receivers {
		p := toENU(sc.receivers[ref], r)
		delayS := math.Hypot(txRef[0]-p[0], txRef[1]-p[1]) / SpeedOfLight
		if i == corrupt {
			delayS += simAnchorNS * 1e-9
		}
		fs := simFs
		if fsFor != nil {
			fs = fsFor(i)
		}
		n := int(simWindowS * fs)
		w := make([]complex128, n)
		pow := 0.0
		for k := range w {
			w[k] = simWaveform(float64(k)/fs-delayS, tones)
			pow += real(w[k])*real(w[k]) + imag(w[k])*imag(w[k])
		}
		pow /= float64(n)
		sigma := math.Sqrt(pow) * math.Pow(10, -simSNRDB/20.0) / math.Sqrt2
		for k := range w {
			w[k] += complex(gauss(rg)*sigma, gauss(rg)*sigma)
		}
		sc.runs[r.ID] = Run{
			ReceiverID: r.ID, AnchorUTC: t0,
			SampleRate: fs, Samples: w,
		}
	}
	return sc
}

func gauss(rg *rand.Rand) float64 {
	return math.Sqrt(-2*math.Log(rg.Float64()+1e-300)) *
		math.Cos(2*math.Pi*rg.Float64())
}

// solveSim drives the full engine path — Buffer.Window, pairwise
// Delay, ChoosePairs, Solve — over a generated scenario at the given
// common solve rate.
func solveSim(t *testing.T, sc simScenario,
	fs float64) (SolveResult, []PairObs) {
	t.Helper()
	buf := NewBuffer(500 * time.Millisecond)
	var ids []string
	for _, r := range sc.receivers {
		buf.Add(sc.runs[r.ID])
		ids = append(ids, r.ID)
	}
	windows, _, ok := buf.Window(ids,
		time.Duration(simWindowS*float64(time.Second)), fs)
	if !ok {
		t.Fatal("buffer could not align the common window")
	}
	pts := make([][2]float64, len(sc.receivers))
	maxBase := 0.0
	for i := range sc.receivers {
		pts[i] = toENU(simOrigin, sc.receivers[i])
		for j := 0; j < i; j++ {
			if d := dist(pts[i], pts[j]); d > maxBase {
				maxBase = d
			}
		}
	}
	maxTau := maxBase/SpeedOfLight*1e9*1.2 + 1000
	var obs []PairObs
	for _, pr := range ChoosePairs(sc.receivers, 15) {
		d, ok := Delay(windows[sc.receivers[pr[0]].ID],
			windows[sc.receivers[pr[1]].ID], maxTau)
		if !ok {
			t.Fatalf("Delay failed for pair %v", pr)
		}
		obs = append(obs, PairObs{
			I: pr[0], J: pr[1], TauNS: d.TauNS, SigmaNS: simSigmaNS,
		})
	}
	return Solve(sc.receivers, obs, Config{AccuracyBudgetM: 50}), obs
}

func simFixError(t *testing.T, res SolveResult, tx [2]float64) float64 {
	t.Helper()
	if res.Fix == nil {
		t.Fatalf("no fix: %s", res.Reason)
	}
	p := toENU(simOrigin, Receiver{Lat: res.Fix.Lat, Lng: res.Fix.Lng})
	return math.Hypot(p[0]-tx[0], p[1]-tx[1])
}
