package location

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestNewTrack(t *testing.T) {
	tr := NewTrack("sig-1")
	if tr.SignalID != "sig-1" {
		t.Fatalf("SignalID = %q, want sig-1", tr.SignalID)
	}
	if tr.FirstSeen.IsZero() || tr.LastSeen.IsZero() {
		t.Fatal("FirstSeen/LastSeen should be initialized")
	}
	if len(tr.Locations) != 0 {
		t.Fatalf("expected no locations, got %d", len(tr.Locations))
	}
}

func TestTrack_SingleLocation_NotMoving(t *testing.T) {
	tr := NewTrack("sig-1")
	loc := SignalLocation{Lat: 50, Lon: 30, Accuracy: 100, Timestamp: t0}
	tr.AddLocation(loc)
	if len(tr.Locations) != 1 {
		t.Fatalf("locations = %d, want 1", len(tr.Locations))
	}
	if !tr.LastSeen.Equal(loc.Timestamp) {
		t.Fatalf("LastSeen = %v, want %v", tr.LastSeen, loc.Timestamp)
	}
	if tr.IsMoving || tr.SpeedKmh != 0 || tr.HeadingDeg != 0 {
		t.Fatalf("single location should not move, got {moving:%v speed:%v heading:%v}",
			tr.IsMoving, tr.SpeedKmh, tr.HeadingDeg)
	}
}

func TestTrack_NoMotion(t *testing.T) {
	tr := NewTrack("sig-1")
	tr.AddLocation(SignalLocation{Lat: 50, Lon: 30, Timestamp: t0})
	tr.AddLocation(SignalLocation{Lat: 50, Lon: 30, Timestamp: t0.Add(time.Second)})
	if tr.IsMoving || tr.SpeedKmh != 0 {
		t.Fatalf("stationary track reports {moving:%v speed:%v}", tr.IsMoving, tr.SpeedKmh)
	}
}

func TestTrack_NorthwardMotion(t *testing.T) {
	tr := NewTrack("sig-1")
	tr.AddLocation(SignalLocation{Lat: 50.0, Lon: 30.0, Timestamp: t0})
	tr.AddLocation(SignalLocation{Lat: 50.01, Lon: 30.0, Timestamp: t0.Add(time.Second)})

	// 0.01 deg of latitude over the haversine with R = 6371 km.
	wantKm := 6371 * (0.01 * math.Pi / 180)
	wantSpeed := wantKm * 3600 // km/h over 1 s
	if math.Abs(tr.SpeedKmh-wantSpeed) > 0.5 {
		t.Fatalf("SpeedKmh = %v, want ~%v", tr.SpeedKmh, wantSpeed)
	}
	if !tr.IsMoving {
		t.Fatal("expected IsMoving = true")
	}
	if tr.HeadingDeg != 0 {
		t.Fatalf("HeadingDeg = %v, want 0 (due north)", tr.HeadingDeg)
	}
}

func TestTrack_EastwardHeading(t *testing.T) {
	tr := NewTrack("sig-1")
	tr.AddLocation(SignalLocation{Lat: 50.0, Lon: 30.0, Timestamp: t0})
	tr.AddLocation(SignalLocation{Lat: 50.0, Lon: 30.01, Timestamp: t0.Add(time.Second)})
	// updateMovement uses 3.14159265 for degrees/radians, so headings
	// carry a ~1e-7 deg error at exact bearings.
	if math.Abs(tr.HeadingDeg-90) > 1e-6 {
		t.Fatalf("HeadingDeg = %v, want ~90 (due east)", tr.HeadingDeg)
	}
}

func TestTrack_HeadingWrapsPositive(t *testing.T) {
	// Southwest: dLat < 0, dLon < 0 → raw heading -135 → wrapped to 225.
	tr := NewTrack("sig-1")
	tr.AddLocation(SignalLocation{Lat: 50.0, Lon: 30.0, Timestamp: t0})
	tr.AddLocation(SignalLocation{Lat: 49.99, Lon: 29.99, Timestamp: t0.Add(time.Second)})
	if math.Abs(tr.HeadingDeg-225) > 1e-6 {
		t.Fatalf("HeadingDeg = %v, want ~225 (southwest)", tr.HeadingDeg)
	}
	if tr.HeadingDeg < 0 || tr.HeadingDeg >= 360 {
		t.Fatalf("HeadingDeg = %v out of [0, 360)", tr.HeadingDeg)
	}
}

func TestTrack_NonPositiveDeltaT(t *testing.T) {
	tr := NewTrack("sig-1")
	tr.AddLocation(SignalLocation{Lat: 50.0, Lon: 30.0, Timestamp: t0})
	// Same timestamp → dt = 0 → no movement update, no division by zero.
	tr.AddLocation(SignalLocation{Lat: 50.01, Lon: 30.0, Timestamp: t0})
	if tr.IsMoving || tr.SpeedKmh != 0 {
		t.Fatalf("dt=0 track reports {moving:%v speed:%v}", tr.IsMoving, tr.SpeedKmh)
	}
}