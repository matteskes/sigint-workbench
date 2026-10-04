package main

import (
	"math"
	"testing"
)

func TestFullScaleDB(t *testing.T) {
	// Full-scale sinusoid through the §5.2 scale: 20·log10(N/2).
	got := fullScaleDB(1024)
	if math.Abs(got-54.19) > 0.01 {
		t.Fatalf("fullScaleDB(1024) = %.2f, want ≈54.19", got)
	}
}

func TestPowerAdvisories(t *testing.T) {
	fs := fullScaleDB(1024)

	// The stage-2 live runs (mean 53.2/52.9) sit within 3 dB of full
	// scale — the clipping warning is legitimate there, and the old
	// mean > −3 threshold made it fire on every healthy run (§ defect:
	// scale mismatch, found on hardware).
	clip := powerAdvisories(53.20, 1.97, fs)
	if len(clip) != 1 || !contains(clip, "clipping") {
		t.Fatalf("53.20 dB at gain 40 should warn clipping only, got %v", clip)
	}

	healthy := powerAdvisories(46.3, 1.7, fs)
	if len(healthy) != 0 {
		t.Fatalf("46.3 dB ±1.7 is a clean run, got %v", healthy)
	}

	unstable := powerAdvisories(30, 4.2, fs)
	if len(unstable) != 1 || !contains(unstable, "varied > 3 dB") {
		t.Fatalf("sd 4.2 should warn instability only, got %v", unstable)
	}

	weak := powerAdvisories(-65, 1.0, fs)
	if len(weak) != 1 || !contains(weak, "would not emit") {
		t.Fatalf("−65 dB should note the §5.4 threshold only, got %v", weak)
	}

	// Boundary: exactly 3 dB below full scale does not warn yet.
	if got := powerAdvisories(fs-3, 1.0, fs); len(got) != 0 {
		t.Fatalf("full-scale − 3 exactly should not warn, got %v", got)
	}
}

func contains(list []string, sub string) bool {
	for _, s := range list {
		if len(s) >= len(sub) && (func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})() {
			return true
		}
	}
	return false
}
