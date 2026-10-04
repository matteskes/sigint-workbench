// Package location — sliding-window two-SDR pair tracking (§8).
//
// The PairTracker correlates per-SDR peak observations in a sliding
// time window and runs candidate pairs through the §8.2 Verifier
// decision table. Verified pairs are returned to the caller so the
// signal-processor can latch the verified flag and re-broadcast.
package location

import (
	"fmt"
	"sync"
	"time"
)

// Observation is one detected peak as seen by one SDR.
type Observation struct {
	SDRID    string    // detecting receiver
	SignalID string    // db.SignalID(sdrID, freqHz) — the row this peak belongs to
	FreqHz   uint64    // absolute signal frequency in Hz
	PowerDB  float64   // measured power in dB
	At       time.Time // detection time
}

// PairResult is a verified two-SDR match (§8.2). A is the observation
// that triggered the pairing, B its partner from another SDR.
type PairResult struct {
	A, B Observation
	Ver  *Verification
}

// PairTracker tracks recent observations per SDR and verifies
// cross-SDR pairs. Safe for concurrent use.
type PairTracker struct {
	verifier *Verifier
	window   time.Duration // observations older than this are dropped
	cooldown time.Duration // min spacing between verified results per pair+bucket

	mu       sync.Mutex
	obs      map[string][]Observation // SDR ID -> recent observations (append order)
	lastEmit map[string]time.Time     // "sdrA|sdrB|bucket" -> last verified At
}

// NewPairTracker creates a tracker around v. The sliding window is
// twice the verifier's MaxTimeDiff (both sides may drift); verified
// results for the same (pair, 50 kHz bucket) are suppressed for 10 s
// so a steady carrier does not spam re-verifications.
func NewPairTracker(v *Verifier) *PairTracker {
	w := 2 * v.MaxTimeDiff
	if w <= 0 {
		w = 4 * time.Second
	}
	return &PairTracker{
		verifier: v,
		window:   w,
		cooldown: 10 * time.Second,
		obs:      make(map[string][]Observation),
		lastEmit: make(map[string]time.Time),
	}
}

// bucketKey builds the cooldown key for a candidate pair: SDR IDs in
// stable order plus a 50 kHz frequency bucket.
func bucketKey(a, b string, freqHz uint64) string {
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	return fmt.Sprintf("%s|%s|%d", lo, hi, freqHz/50_000)
}

// Observe records o and returns verified cross-SDR pairings it forms
// with recent observations from other SDRs (§8.2). Unverified
// candidates are silently dropped; a verified pair suppresses further
// results for the same (pair, bucket) until the cooldown expires.
func (t *PairTracker) Observe(o Observation) []PairResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	win := append(t.obs[o.SDRID], o)
	t.obs[o.SDRID] = pruneObservations(win, o.At, t.window)

	var results []PairResult
	for sdrID, other := range t.obs {
		if sdrID == o.SDRID {
			continue // a signal cannot verify against itself
		}
		cand, ok := closestInTime(other, o.At, t.verifier.MaxTimeDiff)
		if !ok {
			continue
		}
		key := bucketKey(o.SDRID, sdrID, o.FreqHz)
		if last, ok := t.lastEmit[key]; ok && o.At.Sub(last) < t.cooldown {
			continue
		}
		ver, err := t.verifier.Verify(o.SignalID, o.SDRID, sdrID,
			o.FreqHz, cand.FreqHz, o.PowerDB, cand.PowerDB, o.At, cand.At)
		if err != nil || ver == nil || !ver.Verified {
			continue
		}
		t.lastEmit[key] = o.At
		results = append(results, PairResult{A: o, B: cand, Ver: ver})
	}
	return results
}

// pruneObservations drops observations older than window relative to
// now (sliding window maintenance).
func pruneObservations(obs []Observation, now time.Time, window time.Duration) []Observation {
	keep := obs[:0]
	for _, ob := range obs {
		if now.Sub(ob.At) <= window {
			keep = append(keep, ob)
		}
	}
	return keep
}

// closestInTime returns the observation nearest in time to at, within
// maxDiff; ok is false when none qualifies.
func closestInTime(obs []Observation, at time.Time, maxDiff time.Duration) (Observation, bool) {
	best := Observation{}
	bestDiff := maxDiff + time.Nanosecond
	found := false
	for _, ob := range obs {
		d := ob.At.Sub(at)
		if d < 0 {
			d = -d
		}
		if d <= maxDiff && d < bestDiff {
			best, bestDiff, found = ob, d, true
		}
	}
	return best, found
}
