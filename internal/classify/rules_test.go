// Package classify — rules-classifier tests.
//
// B4 regression guard: the VHF airband (118-137 MHz) is AM voice in
// ~25 kHz channels; the old rule labeled any peak in the range
// FM/WFM at 0.85 confidence, so noise spikes published as confident
// "aviation wideband FM" (docs/UI-BUGCHECK.md B4).
package classify

import "testing"

// A voice-channel-width airband peak classifies AM / aviation at
// moderate confidence (121.500 MHz is the international air
// distress frequency).
func TestRulesAirbandIsAM(t *testing.T) {
	rc := NewRuleClassifier()
	res := rc.Classify(121_500_000, 25_000, nil)
	if res.Modulation != "AM" {
		t.Fatalf("modulation = %q, want AM (was WFM — B4)", res.Modulation)
	}
	if res.SubType != "" {
		t.Errorf("subType = %q, want empty", res.SubType)
	}
	if res.Source != "aviation" {
		t.Errorf("source = %q, want aviation", res.Source)
	}
	if res.Confidence != 0.6 {
		t.Errorf("confidence = %v, want 0.6", res.Confidence)
	}
	if res.Method != "rules" || res.Frequency != 121_500_000 {
		t.Errorf("result metadata wrong: %+v", res)
	}
}

// Airband-range peaks that do not look like AM voice must fall back
// to Unknown: a wide burst (200 kHz) or the single-bin spike
// estimateBandwidth reports as bandwidth 0 (the classic noise-shape
// false positive from the bugcheck).
func TestRulesAirbandBandwidthGuard(t *testing.T) {
	rc := NewRuleClassifier()
	for _, bw := range []float64{0, 200_000} {
		res := rc.Classify(125_000_000, bw, nil)
		if res.Modulation != "Unknown" || res.Source != "unknown" {
			t.Errorf("bw %v: got %s/%s, want Unknown/unknown",
				bw, res.Modulation, res.Source)
		}
		if res.Confidence != 0.3 {
			t.Errorf("bw %v: confidence %v, want the 0.3 default", bw, res.Confidence)
		}
	}
}

// The B4 rule change must not disturb the neighboring bands.
func TestRulesNeighboringBandsUnchanged(t *testing.T) {
	rc := NewRuleClassifier()

	// FM broadcast stays high-confidence WFM.
	res := rc.Classify(98_100_000, 180_000, nil)
	if res.Modulation != "FM" || res.SubType != "WFM" ||
		res.Source != "broadcast" || res.Confidence != 0.95 {
		t.Fatalf("FM broadcast: got %+v", res)
	}

	// Marine band (ch. 16, 156.800 MHz) keeps FM/WFM marine.
	res = rc.Classify(156_800_000, 25_000, nil)
	if res.Modulation != "FM" || res.SubType != "WFM" || res.Source != "marine" {
		t.Fatalf("marine: got %+v", res)
	}
}
