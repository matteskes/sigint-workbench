package sdr

import "testing"

func TestSeqTracker_InOrder(t *testing.T) {
	tr := NewSeqTracker()
	for i := 0; i < 5; i++ {
		st := tr.Observe("a", uint64(i))
		if st.Gaps != 0 || st.GapEvents != 0 || st.Reordered != 0 {
			t.Fatalf("in-order frame %d: %+v", i, st)
		}
	}
	st := tr.Snapshot()["a"]
	if st.Frames != 5 || st.LastSeq != 4 {
		t.Errorf("frames/last = %d/%d, want 5/4", st.Frames, st.LastSeq)
	}
}

func TestSeqTracker_Gap(t *testing.T) {
	tr := NewSeqTracker()
	tr.Observe("a", 0)
	st := tr.Observe("a", 5) // seqs 1-4 missing
	if st.Gaps != 4 || st.GapEvents != 1 {
		t.Errorf("gaps/events = %d/%d, want 4/1", st.Gaps, st.GapEvents)
	}
	st = tr.Observe("a", 6) // catch-up is in order, no new events
	if st.Gaps != 4 || st.GapEvents != 1 || st.Reordered != 0 {
		t.Errorf("after catch-up: %+v", st)
	}
}

func TestSeqTracker_ReorderedAndDuplicate(t *testing.T) {
	tr := NewSeqTracker()
	tr.Observe("a", 0)
	tr.Observe("a", 1)
	st := tr.Observe("a", 1) // duplicate
	if st.Reordered != 1 || st.LastSeq != 1 {
		t.Errorf("duplicate: %+v", st)
	}
	st = tr.Observe("a", 3) // gap
	if st.Gaps != 1 || st.GapEvents != 1 {
		t.Errorf("gap: %+v", st)
	}
	st = tr.Observe("a", 2) // late arrival (reorder), LastSeq stays 3
	if st.Reordered != 2 || st.LastSeq != 3 {
		t.Errorf("reorder: %+v", st)
	}
}

// §4.5: seq starts at 0 — a zero after a non-zero high-water mark is
// a sender restart, not mass reordering.
func TestSeqTracker_RestartAtZero(t *testing.T) {
	tr := NewSeqTracker()
	tr.Observe("a", 998)
	tr.Observe("a", 999)
	st := tr.Observe("a", 0)
	if st.Reordered != 0 {
		t.Errorf("restart counted as reorder: %+v", st)
	}
	if st.GapEvents != 1 || st.LastSeq != 0 {
		t.Errorf("restart not re-baselined: %+v", st)
	}
	st = tr.Observe("a", 1)
	if st.Reordered != 0 || st.Gaps != 0 || st.LastSeq != 1 {
		t.Errorf("post-restart stream not in order: %+v", st)
	}
}

func TestSeqTracker_PerSenderIsolation(t *testing.T) {
	tr := NewSeqTracker()
	tr.Observe("a", 10)
	st := tr.Observe("b", 0)
	if st.Frames != 1 || st.LastSeq != 0 {
		t.Errorf("sender b polluted by a: %+v", st)
	}
	if snap := tr.Snapshot(); len(snap) != 2 {
		t.Fatalf("senders = %d, want 2", len(snap))
	} else {
		// Snapshot must return copies, not internal state.
		a := snap["a"]
		a.Frames = 0
		if tr.Snapshot()["a"].Frames != 1 {
			t.Error("Snapshot leaks internal state")
		}
	}
}