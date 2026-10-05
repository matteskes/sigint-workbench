package sdr

import (
	"log"
	"sync"
)

// SeqStats is one sender's sequence accounting (§4.5): v2 frames
// carry seq (per-sender, starts at 0, +1 per frame); gaps and
// reorders are counted from it. sample_index remains authoritative
// for contiguity — seq only reports.
type SeqStats struct {
	Frames    uint64 // frames observed from this sender
	Gaps      uint64 // missing frames inferred (sum of gap widths)
	GapEvents uint64 // distinct contiguous-run breaks (incl. restarts)
	Reordered uint64 // datagrams arriving at or below the high-water seq
	LastSeq   uint64 // high-water seq (meaningful once Frames > 0)
}

// SeqTracker accumulates per-sender §4.5 seq counters. Observers feed
// v2 frames only — a v1 frame's zero seq carries no ordering
// information (§4.5), so consumers gate on frame.V2.
type SeqTracker struct {
	mu    sync.Mutex
	stats map[string]*SeqStats
}

// NewSeqTracker returns an empty tracker.
func NewSeqTracker() *SeqTracker {
	return &SeqTracker{stats: make(map[string]*SeqStats)}
}

// Observe records one frame from sdrID with the given seq and returns
// the sender's stats snapshot. A seq of 0 after a non-zero high-water
// mark is treated as a sender restart (sdr-capture restarted its
// stream, §4.5 seq starts at 0), not as mass reordering.
func (t *SeqTracker) Observe(sdrID string, seq uint64) SeqStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.stats[sdrID]
	if s == nil {
		s = &SeqStats{}
		t.stats[sdrID] = s
	}
	s.Frames++
	if s.Frames == 1 { // first frame from this sender: baseline
		s.LastSeq = seq
		return *s
	}
	if seq == 0 && s.LastSeq > 0 {
		// Stream restart: re-baseline.
		s.GapEvents++
		s.LastSeq = 0
		return *s
	}
	switch {
	case seq == s.LastSeq+1: // in order
	case seq > s.LastSeq+1: // gap
		s.Gaps += seq - s.LastSeq - 1
		s.GapEvents++
	default: // reorder or duplicate (seq <= LastSeq)
		s.Reordered++
	}
	if seq > s.LastSeq {
		s.LastSeq = seq
	}
	return *s
}

// Snapshot returns a copy of the per-sender stats.
func (t *SeqTracker) Snapshot() map[string]SeqStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]SeqStats, len(t.stats))
	for id, s := range t.stats {
		out[id] = *s
	}
	return out
}

// LogGaps writes one line per sender with observed gaps or
// reordering. Callers tick it periodically; clean senders stay
// silent.
func (t *SeqTracker) LogGaps() {
	for id, st := range t.Snapshot() {
		if st.Gaps > 0 || st.Reordered > 0 {
			log.Printf("seq: %s: %d frames, %d gaps (%d events), %d reordered",
				id, st.Frames, st.Gaps, st.GapEvents, st.Reordered)
		}
	}
}