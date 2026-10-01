package db

import (
	"testing"

	"github.com/google/uuid"
)

func TestSignalIDDeterministic(t *testing.T) {
	a := SignalID("rtlsdr-0", 146_520_000)
	b := SignalID("rtlsdr-0", 146_520_000)
	if a != b {
		t.Fatalf("expected deterministic ID, got %q != %q", a, b)
	}
	if _, err := uuid.Parse(a); err != nil {
		t.Fatalf("expected valid UUID, got %q: %v", a, err)
	}
	if uuid.MustParse(a).Version() != 5 {
		t.Fatalf("expected UUIDv5, got version %d", uuid.MustParse(a).Version())
	}
}

func TestSignalIDFrequencyBucket(t *testing.T) {
	// Drift within the same 10 kHz bucket maps to the same ID.
	a := SignalID("rtlsdr-0", 146_524_999)
	b := SignalID("rtlsdr-0", 146_520_001)
	if a != b {
		t.Fatalf("expected same ID within bucket: %q != %q", a, b)
	}
	// Crossing the bucket boundary yields a different ID.
	c := SignalID("rtlsdr-0", 146_530_000)
	if a == c {
		t.Fatalf("expected different ID across bucket boundary: %q == %q", a, c)
	}
}

func TestSignalIDDistinctSDRs(t *testing.T) {
	a := SignalID("rtlsdr-0", 146_520_000)
	b := SignalID("rtlsdr-1", 146_520_000)
	if a == b {
		t.Fatalf("expected different IDs for different SDRs, got %q", a)
	}
}