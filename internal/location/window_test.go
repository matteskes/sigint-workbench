package location

import (
	"testing"
	"time"
)

func obs(sdr string, hz uint64, at time.Time) Observation {
	return Observation{SDRID: sdr, SignalID: "sig-" + sdr, FreqHz: hz, PowerDB: -50, At: at}
}

func TestPairTracker_VerifiesPair(t *testing.T) {
	tr := NewPairTracker(NewVerifier())
	t0 := time.Now()
	if res := tr.Observe(obs("sdr-a", 146_520_000, t0)); len(res) != 0 {
		t.Fatalf("first observation must not pair, got %d results", len(res))
	}
	res := tr.Observe(obs("sdr-b", 146_521_000, t0.Add(100*time.Millisecond))) // 1 kHz apart
	if len(res) != 1 {
		t.Fatalf("expected 1 verified pair, got %d", len(res))
	}
	r := res[0]
	if !r.Ver.Verified || r.Ver.Confidence < 0.5 {
		t.Fatalf("verification = %+v", r.Ver)
	}
	if r.A.SDRID != "sdr-b" || r.B.SDRID != "sdr-a" {
		t.Fatalf("pair = %s+%s, want sdr-b+sdr-a", r.A.SDRID, r.B.SDRID)
	}
}

func TestPairTracker_FrequencyMismatch(t *testing.T) {
	tr := NewPairTracker(NewVerifier())
	t0 := time.Now()
	tr.Observe(obs("sdr-a", 146_520_000, t0))
	res := tr.Observe(obs("sdr-b", 146_530_000, t0.Add(100*time.Millisecond))) // 10 kHz apart
	if len(res) != 0 {
		t.Fatalf("freq mismatch must not verify, got %d results", len(res))
	}
}

func TestPairTracker_WindowExpiry(t *testing.T) {
	tr := NewPairTracker(NewVerifier())
	t0 := time.Now()
	tr.Observe(obs("sdr-a", 146_520_000, t0))
	// Beyond MaxTimeDiff (2 s) the stale observation no longer pairs.
	res := tr.Observe(obs("sdr-b", 146_520_000, t0.Add(3*time.Second)))
	if len(res) != 0 {
		t.Fatalf("stale observation must not verify, got %d results", len(res))
	}
}

func TestPairTracker_SameSDRNeverPairs(t *testing.T) {
	tr := NewPairTracker(NewVerifier())
	t0 := time.Now()
	tr.Observe(obs("sdr-a", 146_520_000, t0))
	res := tr.Observe(obs("sdr-a", 146_520_000, t0.Add(100*time.Millisecond)))
	if len(res) != 0 {
		t.Fatalf("same SDR must not self-verify, got %d results", len(res))
	}
}

func TestPairTracker_Cooldown(t *testing.T) {
	tr := NewPairTracker(NewVerifier())
	t0 := time.Now()
	tr.Observe(obs("sdr-a", 146_520_000, t0))
	if res := tr.Observe(obs("sdr-b", 146_520_000, t0.Add(100*time.Millisecond))); len(res) != 1 {
		t.Fatal("first pair must verify")
	}
	// Steady carrier: re-detections within the cooldown emit nothing.
	for i := 1; i <= 4; i++ {
		at := t0.Add(time.Duration(i) * 500 * time.Millisecond)
		tr.Observe(obs("sdr-a", 146_520_000, at))
		if res := tr.Observe(obs("sdr-b", 146_520_000, at.Add(100*time.Millisecond))); len(res) != 0 {
			t.Fatalf("cooldown: iteration %d emitted %d results", i, len(res))
		}
	}
	// After the cooldown, fresh observations on both sides verify again.
	late := t0.Add(11 * time.Second)
	tr.Observe(obs("sdr-a", 146_520_000, late))
	if res := tr.Observe(obs("sdr-b", 146_520_000, late.Add(100*time.Millisecond))); len(res) != 1 {
		t.Fatalf("post-cooldown pair must verify, got %d results", len(res))
	}
}
