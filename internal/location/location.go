// Package location provides signal location, tracking, and verification.
package location

import (
	"math"
	"time"
)

// SignalLocation represents a signal's estimated geographic position.
type SignalLocation struct {
	Lat      float64   `json:"lat"`
	Lon      float64   `json:"lon"`
	Accuracy float64   `json:"accuracy"` // meters
	Method   string    `json:"method"`   // "sdr_position", "tdoa", "aoa", "user"
	Timestamp time.Time `json:"timestamp"`
}

// Track represents a tracked signal over time.
type Track struct {
	SignalID   string           `json:"signalId"`
	Locations  []SignalLocation `json:"locations"`
	FirstSeen  time.Time        `json:"firstSeen"`
	LastSeen   time.Time        `json:"lastSeen"`
	IsMoving   bool             `json:"isMoving"`
	SpeedKmh   float64          `json:"speedKmh"`
	HeadingDeg float64          `json:"headingDeg"`
}

// NewTrack creates a new track for a signal.
func NewTrack(signalID string) *Track {
	now := time.Now()
	return &Track{
		SignalID:  signalID,
		FirstSeen: now,
		LastSeen:  now,
	}
}

// AddLocation adds a new location observation to the track.
func (t *Track) AddLocation(loc SignalLocation) {
	t.Locations = append(t.Locations, loc)
	t.LastSeen = loc.Timestamp
	t.updateMovement()
}

// updateMovement recalculates speed and heading from the last two positions.
func (t *Track) updateMovement() {
	if len(t.Locations) < 2 {
		t.IsMoving = false
		t.SpeedKmh = 0
		t.HeadingDeg = 0
		return
	}

	prev := t.Locations[len(t.Locations)-2]
	curr := t.Locations[len(t.Locations)-1]
	dt := curr.Timestamp.Sub(prev.Timestamp).Seconds()
	if dt <= 0 {
		return
	}

	// Haversine distance
	dLat := (curr.Lat - prev.Lat) * (3.14159265 / 180)
	dLon := (curr.Lon - prev.Lon) * (3.14159265 / 180)
	a := sin(dLat/2)*sin(dLat/2) +
		cos(prev.Lat*3.14159265/180)*cos(curr.Lat*3.14159265/180) *
			sin(dLon/2)*sin(dLon/2)
	c := 2 * atan2(sqrt(a), sqrt(1-a))
	distanceKm := 6371 * c // Earth radius in km

	t.SpeedKmh = distanceKm / dt * 3600
	t.HeadingDeg = math.Mod(atan2(dLon, dLat)*180/3.14159265+360, 360)
	t.IsMoving = t.SpeedKmh > 1.0 // threshold: 1 km/h
}

// Verification result from two-SDR cross-check.
type Verification struct {
	SignalID     string  `json:"signalId"`
	Verified     bool    `json:"verified"`
	SDR1         string  `json:"sdr1"`
	SDR2         string  `json:"sdr2"`
	Confidence   float64 `json:"confidence"`
	Timestamp    time.Time `json:"timestamp"`
}