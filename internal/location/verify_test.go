package location

import (
	"math"
	"testing"
	"time"
)

func verify(t *testing.T, v *Verifier, freq1, freq2 uint64,
	power1, power2 float64, dt time.Duration) *Verification {
	t.Helper()
	res, err := v.Verify("sig-1", "sdr-0", "sdr-1", freq1, freq2,
		power1, power2, t0, t0.Add(dt))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return res
}

func TestVerify_SameSDRError(t *testing.T) {
	v := NewVerifier()
	if _, err := v.Verify("sig-1", "sdr-0", "sdr-0", 146e6, 146e6, -50, -50, t0, t0); err == nil {
		t.Fatal("expected error when both SDRs are the same, got nil")
	}
}

func TestVerify_NewVerifierDefaults(t *testing.T) {
	v := NewVerifier()
	if v.MaxTimeDiff != 2*time.Second {
		t.Fatalf("MaxTimeDiff = %v, want 2s", v.MaxTimeDiff)
	}
}

func TestVerify_FrequencyMismatch(t *testing.T) {
	v := NewVerifier()
	// 5 kHz apart → within tolerance (passes frequency check).
	ok := verify(t, v, 146_000_000, 146_005_000, -50, -50, 0)
	if !ok.Verified {
		t.Fatalf("freq diff 5000 Hz should verify, got %+v", ok)
	}
	// 5001 Hz apart → rejected with zero confidence.
	bad := verify(t, v, 146_000_000, 146_005_001, -50, -50, 0)
	if bad.Verified {
		t.Fatalf("freq diff 5001 Hz should not verify, got %+v", bad)
	}
	if bad.Confidence != 0 {
		t.Fatalf("confidence = %v, want 0 for frequency mismatch", bad.Confidence)
	}
}

func TestVerify_TimeMismatch(t *testing.T) {
	v := NewVerifier()
	res := verify(t, v, 146_000_000, 146_000_000, -50, -50, 2*time.Second+time.Millisecond)
	if res.Verified {
		t.Fatalf("observation 2.001s apart should not verify (limit 2s), got %+v", res)
	}
	if res.Confidence != 0.2 {
		t.Fatalf("confidence = %v, want 0.2 for time mismatch", res.Confidence)
	}
}

func TestVerify_ConfidenceByPowerDiff(t *testing.T) {
	v := NewVerifier()
	cases := []struct {
		name         string
		power1, p2   float64
		wantConf     float64
		wantVerified bool
	}{
		{"identical power", -50, -50, 0.9, true},
		{"diff exactly 3 dB", -50, -53, 0.9, true}, // penalty requires > 3.0
		{"diff 3.1 dB", -50, -53.1, 0.7, true},
		{"diff exactly 6 dB", -50, -56, 0.7, true}, // second penalty requires > 6.0
		{"diff 6.1 dB", -50, -56.1, 0.4, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := verify(t, v, 146_000_000, 146_000_000, c.power1, c.p2, 0)
			// Floating point: 0.9-0.2-0.3 is not exactly 0.4.
			if math.Abs(res.Confidence-c.wantConf) > 1e-9 {
				t.Fatalf("confidence = %v, want %v", res.Confidence, c.wantConf)
			}
			if res.Verified != c.wantVerified {
				t.Fatalf("verified = %v, want %v", res.Verified, c.wantVerified)
			}
		})
	}
}

func TestVerify_FieldsPopulated(t *testing.T) {
	v := NewVerifier()
	res := verify(t, v, 146_000_000, 146_000_000, -50, -50, 0)
	if res.SignalID != "sig-1" || res.SDR1 != "sdr-0" || res.SDR2 != "sdr-1" {
		t.Fatalf("unexpected identity fields: %+v", res)
	}
	if res.Timestamp.IsZero() {
		t.Fatal("Timestamp not set")
	}
}