// tdoaEngine — §9.6 wiring of the §9.5 engine (internal/tdoa) into
// the live pipeline. v2 frames are dual-ported from the UDP decode
// loop: the engine band-shifts each receiver's channel onto the
// tracked emission, decimates it to a bandwidth-derived common solve
// rate, and assembles gap-free contiguous runs (seq + sample_index
// continuity) into the alignment buffer. A solve goroutine watches
// the §9.6 trigger (signal persisting ≥ 2 s, ≥ 2 located receivers
// covering, ≤ 1 Hz per signal), measures GCC-PHAT pair delays and
// solves; fixes persist on the reference receiver's signal row and
// every attempt emits a signal.tdoa event (§14.2).
//
// v1 frames are never fed (§4.5: the 1-second timestamp MUST NOT be
// used for TDOA), so the engine is inert until capture emits sdr2.
package main

import (
	"context"
	"log"
	"math"
	"math/cmplx"
	"sort"
	"sync"
	"time"

	"sigint-workbench/internal/config"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/location"
	"sigint-workbench/internal/sdr"
	"sigint-workbench/internal/tdoa"
)

const (
	// signalBucketHz is the §9.1 frequency bucket: tracked-emission
	// identity inside the engine (SignalID uses the same bucket).
	signalBucketHz = 10_000

	// defaultSolveRateHz is the fallback common solve rate — 4× the
	// §6.1 NFM/AM channel bandwidths; WFM (200 kHz) channels may raise
	// tdoa.solve_rate_hz (§9.6: the solve rate is bandwidth-derived).
	defaultSolveRateHz = 250_000

	// sigmaFloorNS maps correlation-peak quality to a pair's 1σ time
	// uncertainty (§9.5 weighting): a full-prominence peak is worth
	// 100 ns (≈ 30 m — matched to the §9.6 accuracy budgets and the
	// decimation stack's sub-sample bias); weaker peaks scale
	// linearly, floored at 10×.
	sigmaFloorNS = 100.0

	// coverageFreshness is how long a receiver's last detection of a
	// signal counts as "covering" (§9.6 trigger). It must exceed the
	// publisher's 2 s per-SDR+freq event throttle.
	coverageFreshness = 5 * time.Second

	// solveCooldown is the ≤ 1 Hz per-signal rate limit (§9.6).
	solveCooldown = time.Second

	// persistGate is the §9.6 trigger: a signal must persist this
	// long before the first solve attempt.
	persistGate = 2 * time.Second

	// runCap caps an open run's length: Window needs ONE gap-free run
	// to cover the whole solve window, and long runs keep that likely
	// while bounding memory (§9.5 alignment buffer).
	runCap = 250 * time.Millisecond
)

// tdoaEngine state. All fields are guarded by mu: Feed runs on the
// UDP loop goroutine, tick/solve on the engine goroutine.
type tdoaEngine struct {
	cfg     tdoaSettings
	pub     *publisher
	buf     *tdoa.Buffer
	db      *db.DB
	mu      sync.Mutex
	open    map[string]*openRun    // receiver ID → open contiguous run
	fixes   map[string]tdoa.LatLng // signal ID → accepted fix (§9.6 guard)
	signals map[uint64]*sigState   // 10 kHz bucket → tracked emission
	lastRun map[uint64]time.Time   // freqHz → last solve attempt
}

// tdoaSettings is the resolved §9.6 configuration (defaults applied).
type tdoaSettings struct {
	window       time.Duration
	horizon      time.Duration
	pairCap      int
	outlierSigma float64
	budgetM      float64
	overwrite    bool
	locusRadiusM float64
	solveRateHz  float64
}

// newTDOASettings resolves the §9.6 config keys and defaults:
// window_ms 10, buffer_horizon_ms 500, pair_cap 15, outlier_sigma 3,
// accuracy_budget_m 200, overwrite_placement true (covariance-gated),
// locus_radius_m 0 (⇒ 3× the pair's baseline), solve_rate_hz 0
// (⇒ bandwidth-derived, defaultSolveRateHz fallback).
func newTDOASettings(c config.TDOAConfig) tdoaSettings {
	s := tdoaSettings{
		window:       time.Duration(c.WindowMS) * time.Millisecond,
		horizon:      time.Duration(c.BufferHorizonMS) * time.Millisecond,
		pairCap:      c.PairCap,
		outlierSigma: c.OutlierSigma,
		budgetM:      c.AccuracyBudgetM,
		overwrite:    c.OverwritePlacement,
		locusRadiusM: c.LocusRadiusM,
		solveRateHz:  c.SolveRateHz,
	}
	if s.window <= 0 {
		s.window = 10 * time.Millisecond
	}
	if s.horizon <= 0 {
		s.horizon = 500 * time.Millisecond
	}
	if s.pairCap <= 0 {
		s.pairCap = 15
	}
	if s.outlierSigma <= 0 {
		s.outlierSigma = 3
	}
	if s.budgetM <= 0 {
		s.budgetM = 200
	}
	if s.solveRateHz <= 0 {
		s.solveRateHz = defaultSolveRateHz
	}
	return s
}

// newTDOAEngine builds the engine. pub provides live signal
// detections, receiver locations and the event/persistence surface;
// database may be nil (event-only operation).
func newTDOAEngine(c config.TDOAConfig, pub *publisher, database *db.DB) *tdoaEngine {
	set := newTDOASettings(c)
	return &tdoaEngine{
		cfg:     set,
		pub:     pub,
		buf:     tdoa.NewBuffer(set.horizon),
		db:      database,
		open:    make(map[string]*openRun),
		fixes:   make(map[string]tdoa.LatLng),
		signals: make(map[uint64]*sigState),
		lastRun: make(map[uint64]time.Time),
	}
}

// openRun is one receiver's in-progress contiguous run: §4.5 v2
// frames, band-shifted onto the tracked emission and decimated to
// the run's common solve rate.
type openRun struct {
	run       tdoa.Run // AnchorUTC/StartSample from the FIRST frame
	center    uint64   // receiver's tuned center frequency
	target    uint64   // emission frequency the channel was shifted to
	rate      float64  // common solve rate this run was decimated to
	phase     float64  // NCO phase carried across frames
	seq       uint64   // last frame's seq
	sampleIdx uint64   // last frame's SampleIndex
	pairs     int      // last frame's pair count
}

// sigState tracks one emission (10 kHz bucket): first/last detection
// drive the §9.6 persistence trigger, recvs the coverage set.
type sigState struct {
	freq  uint64
	first time.Time
	last  time.Time
	bwMax float64
	recvs map[string]time.Time
}

// NoteSignal records a detection (called from publisher.publish for
// every classified event). It is the engine's view of the §9.6
// trigger's "signal persisting" and "receivers covering".
func (e *tdoaEngine) NoteSignal(sdrID string, centerHz, peakHz uint64, bwHz float64) {
	if e == nil {
		return
	}
	now := time.Now()
	bucket := peakHz / signalBucketHz
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.signals[bucket]
	if st == nil {
		st = &sigState{freq: bucket*signalBucketHz + signalBucketHz/2,
			recvs: make(map[string]time.Time)}
		e.signals[bucket] = st
	}
	if st.first.IsZero() {
		st.first = now
	}
	st.last = now
	if bwHz > st.bwMax {
		st.bwMax = bwHz
	}
	st.recvs[sdrID] = now
}

// fixFor reports the accepted fix for a signal, if any. A nil engine
// (tdoa disabled) never reports — safe to call unguarded.
func (e *tdoaEngine) fixFor(signalID string) (tdoa.LatLng, bool) {
	if e == nil {
		return tdoa.LatLng{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fix, ok := e.fixes[signalID]
	return fix, ok
}

// Feed consumes one v2 frame: band-shift + decimate + run assembly.
// Cheap by design (≈ n complex multiplies + a boxcar) so it can run
// inline on the UDP loop; solve work stays on the engine loop.
func (e *tdoaEngine) Feed(f *sdr.IQFrame) {
	if e == nil || f == nil || !f.V2 || len(f.Samples) < 2 {
		return // §4.5: TDOA requires the v2 timing fields
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	target, rate := e.plan(f.FreqHz)
	n := len(f.Samples) / 2
	op := e.open[f.SDRID]
	if op != nil && (f.FreqHz != op.center || target != op.target || rate != op.rate ||
		f.Seq != op.seq+1 || f.SampleIndex != op.sampleIdx+uint64(op.pairs)) {
		// Discontinuity: retune, re-target, §4.5 seq gap or dropped
		// datagrams. Close the run — runs stay gap-free by
		// construction (§9.5 alignment buffer).
		e.closeRun(f.SDRID, op)
		op = nil
	}
	if op == nil {
		op = &openRun{
			center: f.FreqHz, target: target, rate: rate,
			seq: f.Seq, sampleIdx: f.SampleIndex, pairs: n,
			run: tdoa.Run{
				ReceiverID:  f.SDRID,
				AnchorUTC:   f.Timestamp, // §4.5 anchor of the FIRST sample
				StartSample: f.SampleIndex,
				SampleRate:  rate,
			},
		}
		e.open[f.SDRID] = op
	} else {
		op.seq, op.sampleIdx, op.pairs = f.Seq, f.SampleIndex, n
	}

	chunk := shiftDecimate(f.Samples, float64(f.SampleRate), rate,
		float64(op.target)-float64(op.center), &op.phase)
	if len(chunk) == 0 {
		return
	}
	op.run.Samples = append(op.run.Samples, chunk...)
	// Cap run length: longer runs widen the Window choice but memory
	// grows with them; Window needs one run covering the window.
	if op.run.EndTime().Sub(op.run.AnchorUTC) >= runCap {
		e.closeRun(f.SDRID, op)
	}
}

// closeRun flushes an open run into the alignment buffer.
func (e *tdoaEngine) closeRun(id string, op *openRun) {
	if len(op.run.Samples) > 0 {
		e.buf.Add(op.run)
	}
	delete(e.open, id)
}

// plan resolves the shift target and common solve rate for a frame
// from the receiver's tracked emissions (called with mu held). The
// freshest emission within the channel becomes the target; the rate
// is bandwidth-derived (4× the widest tracked bandwidth, clamped).
func (e *tdoaEngine) plan(center uint64) (uint64, float64) {
	now := time.Now()
	var target uint64
	var bw float64
	var best time.Time
	for bucket, st := range e.signals {
		freq := bucket * signalBucketHz
		if math.Abs(float64(freq)-float64(center)) > defaultSolveRateHz/2 {
			continue // emission would alias out of the decimated band
		}
		if now.Sub(st.last) > coverageFreshness {
			continue
		}
		if target == 0 || st.last.After(best) {
			target, bw, best = freq, st.bwMax, st.last
		}
	}
	rate := e.cfg.solveRateHz
	if target != 0 && bw > 0 {
		if derived := 4 * bw; derived > 100_000 && derived < 1_000_000 {
			rate = derived
		}
	}
	if target == 0 {
		target = center // no tracked emission: keep the channel as-is
	}
	return target, rate
}

// shiftDecimate band-shifts n native-rate IQ pairs by offHz (the
// emission offset from the tuned center, §9.5 "band-shifted complex
// baseband") and decimates to rate Hz. phase carries the NCO across
// frames of one run; a boxcar pre-filter keeps out-of-band energy
// from aliasing into the cubic resample.
func shiftDecimate(samples []int16, fsNative, rate, offHz float64,
	phase *float64) []complex128 {
	n := len(samples) / 2
	out := make([]complex128, 0, n)
	if offHz != 0 {
		// A tone at center+offHz moves to 0 under e^{-j2π·offHz·t}.
		d := -2 * math.Pi * offHz / fsNative
		for i := 0; i < n; i++ {
			s := cmplx.Rect(1, *phase) *
				complex(float64(samples[2*i]), float64(samples[2*i+1]))
			out = append(out, s)
			*phase += d
			for *phase > math.Pi {
				*phase -= 2 * math.Pi
			}
			for *phase < -math.Pi {
				*phase += 2 * math.Pi
			}
		}
	} else {
		for i := 0; i < n; i++ {
			out = append(out,
				complex(float64(samples[2*i]), float64(samples[2*i+1])))
		}
	}
	if w := int(math.Round(fsNative / rate)); w > 1 {
		// The boxcar re-times the samples to fsNative/w; resampling
		// from the boxed rate keeps the timebase intact (a plain
		// Resample(out, fsNative, rate) here would run 1/w slow).
		out = boxcar(out, w)
		return tdoa.Resample(out, fsNative/float64(w), rate)
	}
	return tdoa.Resample(out, fsNative, rate)
}

// boxcar is a non-overlapping moving average — a cheap decimation
// anti-alias filter ahead of the cubic resample.
func boxcar(in []complex128, w int) []complex128 {
	if w <= 1 || len(in) < w {
		return in
	}
	out := make([]complex128, 0, len(in)/w)
	for i := 0; i+w <= len(in); i += w {
		var acc complex128
		for _, s := range in[i : i+w] {
			acc += s
		}
		out = append(out, acc/complex(float64(w), 0))
	}
	return out
}

// maxTauNS bounds the physically possible pair delay (§9.5): the
// baseline distance over c, with 20% slack for resample jitter.
func maxTauNS(a, b tdoa.Receiver) float64 {
	return baselineM(a, b) / tdoa.SpeedOfLight * 1e9 * 1.2
}

// baselineM is the great-circle distance between two receivers
// (haversine; the §9.5 ENU projection is good to ~0.3% at the ≤ 50 km
// scales a single-host network works at).
func baselineM(a, b tdoa.Receiver) float64 {
	const r = 6371008.8
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	s := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Lat*math.Pi/180)*math.Cos(b.Lat*math.Pi/180)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(s))
}

// loop runs the §9.6 solve goroutine until ctx is done.
func (e *tdoaEngine) loop(ctx context.Context) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.tick()
		}
	}
}

// tick evaluates the §9.6 trigger for every tracked emission:
// persisting ≥ 2 s, ≥ 2 located receivers covering, ≤ 1 Hz per
// signal, and enough buffered gap-free run for the window.
func (e *tdoaEngine) tick() {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()

	for bucket, st := range e.signals {
		if now.Sub(st.last) > coverageFreshness {
			delete(e.signals, bucket) // emission gone; stop tracking
		}
	}
	for bucket, st := range e.signals {
		freqHz := bucket * signalBucketHz
		if now.Sub(st.first) < persistGate {
			continue // §9.6: signal must persist ≥ 2 s
		}
		if lt, ok := e.lastRun[freqHz]; ok && now.Sub(lt) < solveCooldown {
			continue // §9.6: ≤ 1 Hz per signal
		}
		var ids []string
		for id, t := range st.recvs {
			if now.Sub(t) > coverageFreshness {
				continue
			}
			if _, located := e.pub.locations[id]; !located {
				continue // §9.5: receivers need known positions
			}
			if _, flowing := e.open[id]; !flowing {
				continue // receiver not streaming this target now
			}
			ids = append(ids, id)
		}
		if len(ids) < 2 {
			continue // §9.5: ≥ 2 receivers for even a locus
		}
		sort.Strings(ids) // stable receiver indices across attempts
		e.lastRun[freqHz] = now
		e.solve(freqHz, ids)
	}
}

// solve aligns one window, measures pair delays and delivers the
// result. Called with mu held.
func (e *tdoaEngine) solve(freqHz uint64, ids []string) {
	var rate float64
	receivers := make([]tdoa.Receiver, len(ids))
	for i, id := range ids {
		loc := e.pub.locations[id]
		// Single-host solves share one clock — zero uncertainty
		// (§9.5/§9.6; §16 sync quality arrives in slice 6).
		receivers[i] = tdoa.Receiver{ID: id, Lat: loc.Lat, Lng: loc.Lon}
		if op := e.open[id]; op != nil {
			rate = op.rate
		}
	}
	windows, _, ok := e.buf.Window(ids, e.cfg.window, rate)
	if !ok {
		return // no intersecting gap-free runs yet — retry next tick
	}

	pairs := tdoa.ChoosePairs(receivers, e.cfg.pairCap)
	obs := make([]tdoa.PairObs, 0, len(pairs))
	for _, p := range pairs {
		wd, ok := tdoa.Delay(windows[ids[p[0]]], windows[ids[p[1]]],
			maxTauNS(receivers[p[0]], receivers[p[1]]))
		if !ok {
			continue
		}
		sigma := sigmaFloorNS / math.Max(wd.Peak, 0.1)
		// §9.5 eligibility gate: c·σ stays inside the budget.
		if sigma*1e-9*tdoa.SpeedOfLight > e.cfg.budgetM {
			continue
		}
		obs = append(obs, tdoa.PairObs{I: p[0], J: p[1],
			TauNS: wd.TauNS, SigmaNS: sigma})
	}
	res := tdoa.Solve(receivers, obs, tdoa.Config{
		AccuracyBudgetM: e.cfg.budgetM,
		OutlierSigma:    e.cfg.outlierSigma,
		LocusRadiusM:    e.cfg.locusRadiusM,
		PairCap:         e.cfg.pairCap,
	})
	e.deliver(freqHz, ids, receivers, obs, res)
}

// deliver applies the §9.6 result policy: accepted+gated fixes
// persist on the reference receiver's signal row; every attempt —
// fix, locus, or rejection with reason — emits signal.tdoa.
func (e *tdoaEngine) deliver(freqHz uint64, ids []string,
	receivers []tdoa.Receiver, obs []tdoa.PairObs, res tdoa.SolveResult) {
	now := time.Now()
	payload := tdoaEventPayload{
		SignalID:  db.SignalID(e.refReceiver(res, receivers), freqHz),
		FreqHz:    freqHz,
		At:        now,
		Receivers: ids,
	}
	if len(res.Rejected) > 0 {
		log.Printf("[tdoa] %d Hz: %d/%d pair delays rejected as outliers",
			freqHz, len(res.Rejected), len(obs))
	}
	switch {
	case res.Fix != nil:
		fix := res.Fix
		// §9.5 placement-overwrite gate: persist only when accepted
		// AND (overwrite allowed with a positive-definite covariance,
		// or overwrite_placement is off — event-only otherwise).
		persist := res.Accepted && (!e.cfg.overwrite || fix.CovPosDef)
		payload.Accepted = res.Accepted
		payload.Persisted = persist
		payload.Reference = fix.Reference
		payload.Fix = &tdoaFixPayload{
			Lat: fix.Lat, Lng: fix.Lng,
			ResidualNS: fix.ResidualNS, PairsUsed: fix.PairsUsed,
			MaxBaselineM: fix.MaxBaselineM, CovPosDef: fix.CovPosDef,
		}
		if !res.Accepted {
			payload.Reason = res.Reason
		} else if !persist {
			payload.Reason = "covariance gate held the placement (event-only)"
		}
		if persist {
			id := db.SignalID(fix.Reference, freqHz)
			e.fixes[id] = tdoa.LatLng{Lat: fix.Lat, Lng: fix.Lng}
			e.pub.applyTDOAFix(id, freqHz, fix, now)
		}
		log.Printf("[tdoa] %d Hz fix @ %.6f,%.6f residual %.0f ns (%d pairs, %.0f m baseline) accepted=%v persisted=%v",
			freqHz, fix.Lat, fix.Lng, fix.ResidualNS, fix.PairsUsed,
			fix.MaxBaselineM, res.Accepted, persist)
	case res.Locus != nil:
		// §9.5: a 2-receiver pair yields a hyperbolic locus only —
		// report the case, persist nothing (§9.6).
		payload.Locus = &tdoaLocus{
			Lat1: res.Locus[0].Lat, Lng1: res.Locus[0].Lng,
			Lat2: res.Locus[1].Lat, Lng2: res.Locus[1].Lng,
		}
		payload.Reason = "2 receivers: hyperbolic locus only (no point fix)"
		log.Printf("[tdoa] %d Hz locus (%.6f,%.6f)-(%.6f,%.6f)",
			freqHz, payload.Locus.Lat1, payload.Locus.Lng1,
			payload.Locus.Lat2, payload.Locus.Lng2)
	default:
		payload.Reason = res.Reason
		if payload.Reason == "" {
			payload.Reason = "no geometry from pair delays"
		}
		log.Printf("[tdoa] %d Hz: no fix (%s)", freqHz, payload.Reason)
	}
	e.pub.queue("signal.tdoa", payload)
}

// refReceiver names the receiver a fix/row belongs to: the solve's
// reference when present, else the first receiver (event identity).
func (e *tdoaEngine) refReceiver(res tdoa.SolveResult, receivers []tdoa.Receiver) string {
	if res.Fix != nil {
		return res.Fix.Reference
	}
	if len(receivers) > 0 {
		return receivers[0].ID
	}
	return ""
}

// tdoaEventPayload is the §14.2 signal.tdoa payload (§9.6): signal
// identity, the fix or locus surface, quality, and the receiver set.
type tdoaEventPayload struct {
	SignalID  string          `json:"signalId"`
	FreqHz    uint64          `json:"freqHz"`
	At        time.Time       `json:"at"`
	Accepted  bool            `json:"accepted"`
	Persisted bool            `json:"persisted"`
	Reason    string          `json:"reason,omitempty"`
	Reference string          `json:"reference,omitempty"`
	Receivers []string        `json:"receivers"`
	Fix       *tdoaFixPayload `json:"fix,omitempty"`
	Locus     *tdoaLocus      `json:"locus,omitempty"`
}

// tdoaFixPayload is the quality surface of a point fix (§9.5 step 4).
type tdoaFixPayload struct {
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	ResidualNS   float64 `json:"residualNs"`
	PairsUsed    int     `json:"pairsUsed"`
	MaxBaselineM float64 `json:"maxBaselineM"`
	CovPosDef    bool    `json:"covPosDef"`
}

// tdoaLocus carries the §9.6 locus endpoints
// ({lat1,lng1,lat2,lng2}).
type tdoaLocus struct {
	Lat1 float64 `json:"lat1"`
	Lng1 float64 `json:"lng1"`
	Lat2 float64 `json:"lat2"`
	Lng2 float64 `json:"lng2"`
}

// applyTDOAFix persists an accepted fix on the reference receiver's
// signal row (locked persistence decision, §9.6), feeds §9.4
// tracking, and re-broadcasts the signal payload so the map moves.
func (p *publisher) applyTDOAFix(id string, freqHz uint64,
	fix *tdoa.Fix, now time.Time) {
	p.mu.Lock()
	base := p.lastSig[id]
	tr, ok := p.tracks[id]
	if !ok {
		tr = location.NewTrack(id)
		p.tracks[id] = tr
	}
	notify := now.Sub(p.trackWrite[id]) >= time.Second
	if notify {
		p.trackWrite[id] = now
	}
	p.mu.Unlock()

	sig := &db.Signal{
		ID:       id,
		FreqHz:   freqHz,
		SDRID:    fix.Reference,
		Active:   true,
		LastSeen: now,
		Lat:      &fix.Lat,
		Lon:      &fix.Lng,
		// §9.6 quality columns (migration 004) ride the upsert; the
		// COALESCE conflict clause keeps them sticky afterwards.
		ResidualNS:   &fix.ResidualNS,
		PairsUsed:    &fix.PairsUsed,
		MaxBaselineM: &fix.MaxBaselineM,
		Reference:    &fix.Reference,
	}
	if base != nil {
		// Keep the detection-side fields (classification, power,
		// first seen) from the last published payload.
		sig.BandwidthHz = base.BandwidthHz
		sig.Modulation = base.Modulation
		sig.SubType = base.SubType
		sig.Class = base.Class
		sig.Method = base.Method
		sig.Confidence = base.Confidence
		sig.PowerDBM = base.PowerDBM
		sig.PowerCalibrated = base.PowerCalibrated
		sig.FirstSeen = base.FirstSeen
		sig.Verified = base.Verified
	} else {
		sig.FirstSeen = now
	}
	if p.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := p.db.UpsertSignal(ctx, sig); err != nil {
			log.Printf("tdoa upsert signal %s: %v", id, err)
		}
		cancel()
	}

	// §9.6: TDOA rows feed §9.4 tracking like any placement.
	tr.AddLocation(location.SignalLocation{
		Lat: fix.Lat, Lon: fix.Lng, Method: "tdoa", Timestamp: now,
	})
	if notify {
		p.persistTrack(tr)
		p.queue("track.update", trackSnapshot(tr))
	}
	p.queue("signal.update", sig)
}
