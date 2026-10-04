package record

import (
	"time"
)

// RecordingMeta is the retention input for one stored recording.
type RecordingMeta struct {
	ID        string
	FilePath  string
	StartTime time.Time
	SizeBytes int64
}

// SelectPurge returns the recordings to delete under the §11 limits:
// everything older than maxAge (0 = no age limit), then — oldest
// first — recordings while the survivors exceed maxSizeBytes
// (<=0 = no size cap). Pure: it performs no I/O and never reorders
// the input, so callers can rely on the returned slice being ordered
// oldest-first within the size-cap phase.
func SelectPurge(recs []RecordingMeta, now time.Time, maxAge time.Duration, maxSizeBytes int64) []RecordingMeta {
	var out []RecordingMeta
	var kept []RecordingMeta
	var total int64
	for _, r := range recs {
		if maxAge > 0 && now.Sub(r.StartTime) > maxAge {
			out = append(out, r)
			continue
		}
		kept = append(kept, r)
		total += r.SizeBytes
	}
	if maxSizeBytes > 0 {
		for _, r := range kept {
			if total <= maxSizeBytes {
				break
			}
			out = append(out, r)
			total -= r.SizeBytes
		}
	}
	return out
}
