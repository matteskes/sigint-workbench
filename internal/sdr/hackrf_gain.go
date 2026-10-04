// Package sdr — HackRF gain mapping shared by tagged and untagged builds.
//
// The SDR interface exposes a single gain figure (SetGain, dB), while
// the HackRF has two RX gain stages: LNA (IF) 0-40 dB in 8 dB steps and
// VGA (baseband) 0-62 dB in 2 dB steps, plus a 14 dB amp. The amp stays
// OFF so the gain chain is deterministic — a prerequisite for the §5.6
// calibration contract (SPEC).
package sdr

// Stage limits for the SetGain mapping (§15.4).
const (
	hackRFVGAStepDB = 2
	hackRFVGAMaxDB  = 62
	hackRFLNAStepDB = 8
	hackRFLNAMaxDB  = 40
)

// splitHackRFGain maps one gain figure onto the two RX stages:
// VGA takes up to 62 dB first (2 dB steps); the LNA adds the remainder
// in 8 dB steps up to 40 dB. The mapping is monotonic in db and the
// applied total never exceeds 102 dB (40 LNA + 62 VGA). Negative or
// zero input maps to (0, 0) — SetGain rejects it (HackRF has no auto
// gain mode, unlike the RTL-SDR tuner).
func splitHackRFGain(db float64) (lna, vga uint32) {
	if db <= 0 {
		return 0, 0
	}
	vga = clampGainStep(db, hackRFVGAStepDB, hackRFVGAMaxDB)
	rem := db - float64(vga)
	if rem < 0 {
		rem = 0
	}
	lna = clampGainStep(rem, hackRFLNAStepDB, hackRFLNAMaxDB)
	return lna, vga
}

// clampGainStep rounds v to the nearest multiple of step and clamps
// the result to [0, max].
func clampGainStep(v float64, step, max uint32) uint32 {
	n := int64(v/float64(step) + 0.5)
	if n < 0 {
		n = 0
	}
	g := n * int64(step)
	if g > int64(max) {
		return max
	}
	return uint32(g)
}
