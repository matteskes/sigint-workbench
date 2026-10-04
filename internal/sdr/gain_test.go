package sdr

import "testing"

// r820t2Gains mirrors the table rtlsdr_get_tuner_gains reports for
// R820T/R828D tuners (largest adjacent gap 4.8 dB, max 49.6 dB).
var r820t2Gains = []float64{
	0.0, 0.9, 1.4, 2.7, 3.7, 7.7, 12.5, 14.4, 15.7, 16.6, 19.7, 20.7,
	22.9, 25.4, 28.0, 29.7, 32.8, 33.8, 36.4, 37.2, 38.6, 40.2, 42.1,
	43.4, 43.9, 44.5, 48.0, 49.6,
}

func TestNearestGain(t *testing.T) {
	cases := []struct {
		name string
		db   float64
		want float64
		ok   bool
	}{
		{"exact step", 40.2, 40.2, true},
		{"config default 40 snaps up", 40, 40.2, true},
		{"nearest above", 41.5, 42.1, true},
		{"mid-table hole: nearer step wins", 10, 7.7, true},
		{"mid-table hole: other side", 10.8, 12.5, true},
		{"tie prefers the lower step", 3.2, 2.7, true},
		{"slightly above max snaps down", 51.9, 49.6, true},
		{"§15.3 defect 4: 99 dB rejected", 99, 49.6, false},
		{"far above max rejected", 60, 49.6, false},
		{"far below min rejected", -4, 0, false},
		{"boundary exactly 3 dB accepted", 52.6, 49.6, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nearestGain(r820t2Gains, tc.db)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("nearestGain(%.1f) = %.1f,%v; want %.1f,%v",
					tc.db, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestNearestGainEmptyTablePassesThrough(t *testing.T) {
	// A failed table query must not break gain application — the raw
	// request passes through and the pre-§15.3-fix behavior applies.
	got, ok := nearestGain(nil, 42.7)
	if !ok || got != 42.7 {
		t.Fatalf("nearestGain(nil, 42.7) = %.1f,%v; want 42.7,true", got, ok)
	}
}