// Package db — stable signal identifiers.
//
// Signals detected repeatedly by the same SDR at (approximately) the
// same frequency must map to one database row. SignalID derives a
// deterministic UUIDv5 from the SDR ID and a 10 kHz frequency bucket,
// so small measurement drift does not create duplicates.
package db

import (
	"fmt"

	"github.com/google/uuid"
)

// SignalID returns the deterministic UUID for a signal detected on
// sdrID at approximately freqHz.
func SignalID(sdrID string, freqHz uint64) string {
	bucket := freqHz / 10_000
	name := fmt.Sprintf("sigint-workbench/%s/%d", sdrID, bucket)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name)).String()
}