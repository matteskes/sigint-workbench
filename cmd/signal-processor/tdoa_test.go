// tdoa_test.go — §9.6 wiring tests: a synthetic multi-receiver §4.5
// v2 stream must flow Feed → gap-free runs → aligned window →
// GCC-PHAT → solve → persisted fix + signal.tdoa, surviving a §9.5
// anchor-corrupted receiver, and the §9.6 flip-flop guard must keep
// an accepted fix against later sdr_position placements.
package main

import (
	"encoding/json"
	"math"
	"math/cmplx"
	"net/http"
	"testing"
	"time"

	"sigint-workbench/internal/config"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/sdr"
	"sigint-workbench/internal/tdoa"
)

// tdoaSynthFrames builds one receiver's §4.5 v2 frames observing a
// wideband multitone emission from distM away. Each tone is a
// recursive phasor started at exp(−j2πf·τ), so the fractional delay
// is exact by construction (the §9.5 validation-path synthesis); the
// 200 kHz-wide tone set gives GCC-PHAT a sharp, unambiguous peak.
// corruptNS adds an anchor-offset corruption to every delay (§9.5
// corruption model).
func tdoaSynthFrames(id string, distM float64, frames int, t0 time.Time,
	fs float64, pairs int, corruptNS float64) []*sdr.IQFrame {
	tau := distM/tdoa.SpeedOfLight + corruptNS*1e-9
	type tone struct {
		f float64
	}
	var tones []tone
	for k := 0; k < 12; k++ {
		tones = append(tones, tone{f: -100e3 + float64(k)*18.5e3}) // no DC, ≤ ±100 kHz
	}
	start := make([]complex128, len(tones))
	step := make([]complex128, len(tones))
	for i, tn := range tones {
		// s(t−τ) = e^{−j2πfτ} · s(t): fold the delay into the phase.
		start[i] = cmplx.Exp(complex(0, float64(i)*2.399963-2*math.Pi*tn.f*tau))
		step[i] = cmplx.Exp(complex(0, 2*math.Pi*tn.f/fs))
	}
	amp := complex(8000/math.Sqrt(float64(len(tones))), 0)

	out := make([]*sdr.IQFrame, 0, frames)
	for k := 0; k < frames; k++ {
		samples := make([]int16, pairs*2)
		for n := 0; n < pairs; n++ {
			var s complex128
			for i := range tones {
				start[i] *= step[i]
				s += start[i]
			}
			s *= amp
			samples[2*n] = int16(real(s))
			samples[2*n+1] = int16(imag(s))
		}
		out = append(out, &sdr.IQFrame{
			SDRID:       id,
			FreqHz:      146_520_000,
			SampleRate:  uint32(fs),
			Timestamp:   t0.Add(time.Duration(float64(k*pairs) / fs * float64(time.Second))),
			Samples:     samples,
			V2:          true,
			Seq:         uint64(k),
			SampleIndex: uint64(k * pairs),
		})
	}
	return out
}

// tdoaDistM wraps the engine's haversine for test-side geometry.
func tdoaDistM(a, b tdoa.LatLng) float64 {
	return baselineM(tdoa.Receiver{Lat: a.Lat, Lng: a.Lng},
		tdoa.Receiver{Lat: b.Lat, Lng: b.Lng})
}

// TestTDOAEngineWiring exercises the full §9.6 pipeline: four
// synthetic receivers (one with a deliberately wrong anchor) produce
// a persisted fix within the generous wiring tolerance, plus
// signal.tdoa and signal.update events.
func TestTDOAEngineWiring(t *testing.T) {
	tx := tdoa.LatLng{Lat: 40.7030, Lng: -73.9950}
	// km-scale geometry (§9.5 validation-path scale): pair delays of
	// several bins at the 250 kS/s solve rate (1 bin = 4 µs ≈ 1200 m)
	// so the GCC-PHAT peak search + parabolic refinement operate on
	// real bins instead of collapsing into bin 0.
	poses := map[string]tdoa.LatLng{
		"sim-1": {Lat: 40.6500, Lng: -74.0500}, // corrupted anchor
		"sim-2": {Lat: 40.7500, Lng: -73.9900},
		"sim-3": {Lat: 40.6900, Lng: -73.9000},
		"sim-4": {Lat: 40.7200, Lng: -73.9300},
	}
	locs := map[string]sdrLocation{}
	for id, p := range poses {
		locs[id] = sdrLocation{Lat: p.Lat, Lon: p.Lng}
	}
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	pub := newPublisher(nil, srv.URL, locs, time.Minute)
	e := newTDOAEngine(config.TDOAConfig{
		Enabled:         true,
		WindowMS:        10,
		BufferHorizonMS: 500,
		AccuracyBudgetM: 200,
		SolveRateHz:     1_000_000, // 1 µs bins for km-scale geometry
	}, pub, nil)
	pub.eng = e

	const fs = 2_400_000.0
	const pairs = 1024
	// 640 frames ≈ 273 ms per receiver: the 250 ms run cap closes a
	// buffered run mid-stream while a fresh open run stays live — the
	// exact production flow pattern.
	const frames = 640
	t0 := time.Unix(1700000000, 0)
	for id, pos := range poses {
		corrupt := 0.0
		if id == "sim-1" {
			corrupt = 2000 // §9.5 anchor-offset corruption: +2 µs
		}
		for _, f := range tdoaSynthFrames(id, tdoaDistM(pos, tx),
			frames, t0, fs, pairs, corrupt) {
			e.Feed(f)
		}
	}

	// The publisher would report these detections; note them so the
	// §9.6 trigger state (persistence + coverage) exists.
	for id := range poses {
		e.NoteSignal(id, 146_520_000, 146_520_000+30_000, 12_500)
	}
	// Force the 2 s persistence gate instead of waiting it out.
	e.mu.Lock()
	for _, st := range e.signals {
		st.first = st.first.Add(-3 * time.Second)
	}
	e.mu.Unlock()
	e.tick()

	// An accepted fix persisted on the reference receiver's row
	// (§9.6 persistence decision) and landed near the transmitter —
	// a poisoned fit (corrupted pairs leaking into the solve) lands
	// hundreds of meters off, so this asserts the rejection too.
	e.mu.Lock()
	nFixes := len(e.fixes)
	var fix tdoa.LatLng
	for _, f := range e.fixes {
		fix = f
	}
	e.mu.Unlock()
	if nFixes != 1 {
		t.Fatalf("persisted fixes = %d, want 1", nFixes)
	}
	if d := tdoaDistM(fix, tx); d > 500 {
		t.Errorf("fix is %.0f m from the transmitter, want ≤ 500 m "+
			"(corrupted pairs likely leaked into the solve)", d)
	}

	// signal.tdoa + signal.update rode out to the hub.
	if n := hub.waitCount(2); n < 2 {
		t.Fatalf("expected ≥ 2 hub events, got %d", n)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	var gotTDOA, gotUpdate bool
	for _, ev := range hub.events {
		switch ev.Type {
		case "signal.tdoa":
			gotTDOA = true
			var pl tdoaEventPayload
			if err := json.Unmarshal(ev.Payload, &pl); err != nil {
				t.Fatalf("unmarshal signal.tdoa payload: %v", err)
			}
			if !pl.Accepted || !pl.Persisted {
				t.Errorf("signal.tdoa accepted/persisted = %v/%v, want true/true (reason %q)",
					pl.Accepted, pl.Persisted, pl.Reason)
			}
			if pl.Fix == nil || len(pl.Receivers) != len(poses) {
				t.Errorf("signal.tdoa payload missing fix or receivers: %+v", pl)
			}
			if pl.Reference == "" {
				t.Errorf("signal.tdoa payload missing reference: %+v", pl)
			}
		case "signal.update":
			gotUpdate = true
		}
	}
	if !gotTDOA || !gotUpdate {
		t.Errorf("hub events missing signal.tdoa (%v) or signal.update (%v)",
			gotTDOA, gotUpdate)
	}
}

// TestTDOAFlipFlopGuard — §9.6: once an accepted fix owns a signal's
// placement, later sdr_position publishes must not clobber it.
func TestTDOAFlipFlopGuard(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	// The detecting SDR sits far from where the fix will land.
	p := newPublisher(nil, srv.URL,
		map[string]sdrLocation{"rtlsdr-0": {Lat: 10.0, Lon: 20.0}},
		time.Minute)
	// Kill keep-alives: the httptest server may close the idle
	// connection between posts, and the worker would drop the
	// in-flight event on the reset.
	p.client = &http.Client{Timeout: 2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true}}
	e := newTDOAEngine(config.TDOAConfig{Enabled: true}, p, nil)
	p.eng = e
	ev := testEvent()
	now := time.Now()

	// Before any fix, placement follows the SDR row (§9.3).
	p.publish(ev, now)
	if n := hub.waitCount(1); n < 1 {
		t.Fatalf("expected the first placement event, got %d", n)
	}
	var first struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	}
	hub.mu.Lock()
	if err := json.Unmarshal(hub.events[0].Payload, &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hub.mu.Unlock()
	if first.Lat != 10.0 || first.Lon != 20.0 {
		t.Fatalf("pre-fix placement = %v,%v, want the SDR position", first.Lat, first.Lon)
	}

	// An accepted fix lands 55 km away.
	fix := tdoa.LatLng{Lat: 10.5, Lng: 20.5}
	e.mu.Lock()
	e.fixes[db.SignalID(ev.SDRID, ev.PeakHz)] = fix
	e.mu.Unlock()
	p.publish(ev, now.Add(3*time.Second))

	// Poll for the post-fix update specifically — the async worker
	// may still be draining p1's track.update first.
	deadline := time.Now().Add(2 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		for _, e := range hub.events {
			if e.Type == "signal.update" {
				last = nil
				if err := json.Unmarshal(e.Payload, &last); err != nil {
					hub.mu.Unlock()
					t.Fatalf("unmarshal update: %v", err)
				}
			}
		}
		hub.mu.Unlock()
		if last != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if last == nil {
		t.Fatal("no signal.update after the fix")
	}
	if lat := last["lat"].(float64); math.Abs(lat-fix.Lat) > 1e-9 {
		t.Errorf("post-fix lat = %v, want the fix %v (clobbered by sdr_position)", lat, fix.Lat)
	}
	if lon := last["lon"].(float64); math.Abs(lon-fix.Lng) > 1e-9 {
		t.Errorf("post-fix lon = %v, want the fix %v (clobbered by sdr_position)", lon, fix.Lng)
	}
}

// TestTDOAEngineIgnoresV1 — §4.5: v1 frames carry no sample-accurate
// timing, so the engine must stay inert (no runs, no solves, no
// events) until capture emits sdr2.
func TestTDOAEngineIgnoresV1(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	pub := newPublisher(nil, srv.URL,
		map[string]sdrLocation{
			"sim-1": {Lat: 40.70, Lon: -74.00},
			"sim-2": {Lat: 40.75, Lon: -74.00},
		}, time.Minute)
	e := newTDOAEngine(config.TDOAConfig{
		Enabled: true, WindowMS: 10, BufferHorizonMS: 500,
	}, pub, nil)
	pub.eng = e

	t0 := time.Unix(1700000000, 0)
	for k := 0; k < 48; k++ {
		for _, id := range []string{"sim-1", "sim-2"} {
			f := &sdr.IQFrame{
				SDRID:       id,
				FreqHz:      146_520_000,
				SampleRate:  2_400_000,
				Timestamp:   t0.Add(time.Duration(k) * 427 * time.Microsecond),
				Samples:     make([]int16, 2048),
				Seq:         uint64(k),     // v1: decoders leave these 0
				SampleIndex: uint64(k * 1024), // …and Feed must not care
			}
			f.V2 = false // classic §4.1 frame
			e.Feed(f)
		}
	}
	e.NoteSignal("sim-1", 146_520_000, 146_520_000, 12_500)
	e.NoteSignal("sim-2", 146_520_000, 146_520_000, 12_500)
	e.mu.Lock()
	for _, st := range e.signals {
		st.first = st.first.Add(-3 * time.Second)
	}
	e.mu.Unlock()
	e.tick()

	time.Sleep(50 * time.Millisecond)
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for _, ev := range hub.events {
		if ev.Type == "signal.tdoa" {
			t.Fatalf("signal.tdoa emitted from v1 frames: %+v", ev)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.open) != 0 {
		t.Errorf("v1 frames produced %d open runs, want 0", len(e.open))
	}
}