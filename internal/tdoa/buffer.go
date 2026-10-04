package tdoa

import (
	"math"
	"sort"
	"time"
)

// Buffer is the §9.5 alignment buffer: per-receiver contiguous runs
// over a bounded horizon. Runs are gap-free by construction — the
// caller assembles them from §4.5 v2 sample_index — so a receiver
// serves a window only when a single run covers it; any real gap
// simply excludes that interval from solving.
type Buffer struct {
	horizon time.Duration
	runs    map[string][]Run
}

// NewBuffer returns an empty buffer pruning runs older than horizon
// (tdoa.buffer_horizon_ms, §9.6). horizon ≤ 0 disables pruning.
func NewBuffer(horizon time.Duration) *Buffer {
	return &Buffer{horizon: horizon, runs: make(map[string][]Run)}
}

// Add stores one run, keeping each receiver's runs sorted by anchor
// and pruning runs that can no longer fall inside the horizon
// measured from the newest run's end (§9.5 horizon prune).
func (b *Buffer) Add(r Run) {
	rs := append(b.runs[r.ReceiverID], r)
	sort.Slice(rs, func(i, j int) bool {
		return rs[i].AnchorUTC.Before(rs[j].AnchorUTC)
	})
	if b.horizon > 0 && len(rs) > 0 {
		cutoff := rs[len(rs)-1].EndTime().Add(-b.horizon)
		keep := rs[:0]
		for _, run := range rs {
			if run.EndTime().After(cutoff) {
				keep = append(keep, run)
			}
		}
		rs = keep
	}
	b.runs[r.ReceiverID] = rs
}

// Window aligns a common [t0, t0+span) observation across ids at
// sample rate fs (resampling runs whose rate differs, §9.5
// mixed-rate prerequisite), choosing the latest fully covered span.
// It reports false when any receiver cannot serve the span — e.g.
// where a §4.5 gap splits its runs.
func (b *Buffer) Window(ids []string, span time.Duration,
	fs float64) (map[string]Window, time.Time, bool) {
	if fs <= 0 || span <= 0 {
		return nil, time.Time{}, false
	}
	// Intersect the receivers' coverage (§9.5 step 1): the window
	// starts at the latest common coverage end minus the span.
	lo, hi := time.Time{}, time.Time{}
	for _, id := range ids {
		rs := b.runs[id]
		if len(rs) == 0 {
			return nil, time.Time{}, false
		}
		s, e := rs[0].AnchorUTC, rs[0].EndTime()
		for _, r := range rs[1:] {
			if r.AnchorUTC.Before(s) {
				s = r.AnchorUTC
			}
			if re := r.EndTime(); re.After(e) {
				e = re
			}
		}
		if lo.IsZero() || s.After(lo) {
			lo = s
		}
		if hi.IsZero() || e.Before(hi) {
			hi = e
		}
	}
	if hi.Sub(lo) < span {
		return nil, time.Time{}, false
	}
	t0 := hi.Add(-span)
	n := int(math.Round(span.Seconds() * fs))
	out := make(map[string]Window, len(ids))
	for _, id := range ids {
		src, ok := b.runCovering(id, t0, t0.Add(span))
		if !ok {
			return nil, time.Time{}, false
		}
		sub, ok := src.Slice(t0, t0.Add(span))
		if !ok {
			return nil, time.Time{}, false
		}
		samples := sub.Samples
		if math.Abs(sub.SampleRate-fs) > 1e-9*fs {
			samples = Resample(samples, sub.SampleRate, fs)
		}
		for len(samples) < n { // resample rounding guard
			samples = append(samples, 0)
		}
		out[id] = Window{ReceiverID: id, SampleRate: fs,
			Samples: samples[:n]}
	}
	return out, t0, true
}

// runCovering finds a single gap-free run covering [t0, t1).
func (b *Buffer) runCovering(id string, t0, t1 time.Time) (Run, bool) {
	for _, r := range b.runs[id] {
		if !r.AnchorUTC.After(t0) && !r.EndTime().Before(t1) {
			return r, true
		}
	}
	return Run{}, false
}
