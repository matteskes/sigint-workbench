// Package sdr — RTL-SDR tuner gain validation.
//
// The r82xx tuner accepts any rtlsdr_set_tuner_gain figure and
// silently clamps out-of-table requests to its maximum (~49.6 dB):
// the call returns success while the hardware runs at roughly half
// the configured gain, and status/§5.6 applied_gain_db would report
// the configured figure (§15.3 defect 4, found by the V8 check on
// hardware — docs/HARDWARE.md §5). SetGain therefore validates every
// manual gain against the tuner's supported table before applying
// it: requests within gainSnapMaxDeltaDB of a supported step snap to
// that step; anything further is rejected so sdr-capture aborts
// startup (§15.3 fix 3) instead of misreporting dBm.
package sdr

// gainSnapMaxDeltaDB is the largest |requested − supported| deviation
// nearestGain accepts. The R820T2 gain table's largest adjacent gap is
// 4.8 dB, so any request inside the table's span lands within 2.4 dB
// of a step; only requests beyond the span by more than that fail.
const gainSnapMaxDeltaDB = 3.0

// nearestGain returns the supported tuner gain closest to db. ok is
// false when db is more than gainSnapMaxDeltaDB away from every
// supported step — the request is not achievable and must be rejected
// rather than silently snapped. With no table (query failed), db
// passes through unchanged so behavior matches the raw driver.
func nearestGain(supported []float64, db float64) (nearest float64, ok bool) {
	if len(supported) == 0 {
		return db, true
	}
	nearest = supported[0]
	bestDelta := absDB(db - nearest)
	for _, g := range supported[1:] {
		if d := absDB(db - g); d < bestDelta {
			nearest, bestDelta = g, d
		}
	}
	return nearest, bestDelta <= gainSnapMaxDeltaDB
}

func absDB(d float64) float64 {
	if d < 0 {
		return -d
	}
	return d
}