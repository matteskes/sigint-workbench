package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"sigint-workbench/internal/classify"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
	"sigint-workbench/internal/ws"
)

// fakeHub records events posted to the ingest endpoint.
type fakeHub struct {
	mu     sync.Mutex
	events []ws.Event
}

func (f *fakeHub) serve() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var ev ws.Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.events = append(f.events, ev)
		f.mu.Unlock()
		w.Write([]byte(`{"status":"accepted"}`))
	}))
	return srv
}

func (f *fakeHub) waitCount(n int) int {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		c := len(f.events)
		f.mu.Unlock()
		if c >= n {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func testEvent() signalEvent {
	return signalEvent{
		SDRID:     "rtlsdr-0",
		Timestamp: time.Now(),
		CenterHz:  146_000_000,
		PeakHz:    146_520_000,
		PowerDB:   -20,
		Bandwidth: 25_000,
		Class: &classify.Result{
			Modulation: "am",
			Source:     "aviation",
			Confidence: 0.9,
			Bandwidth:  25_000,
		},
	}
}

func TestPublishNewThenUpdate(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	locs := map[string]sdrLocation{"rtlsdr-0": {Lat: 40.7, Lon: -74.0}}
	p := newPublisher(nil, srv.URL, locs, time.Minute)

	now := time.Now()
	p.publish(testEvent(), now)
	p.publish(testEvent(), now.Add(3*time.Second))

	if n := hub.waitCount(2); n < 2 {
		t.Fatalf("expected 2 events, got %d", n)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	// §9.4 adds interleaved track.update events; find the first
	// signal.new and the first signal.update and assert their order.
	newIdx, updIdx := -1, -1
	for i, ev := range hub.events {
		switch ev.Type {
		case "signal.new":
			if newIdx == -1 {
				newIdx = i
			}
		case "signal.update":
			if updIdx == -1 {
				updIdx = i
			}
		}
	}
	if newIdx == -1 || updIdx == -1 || newIdx > updIdx {
		t.Fatalf("want signal.new before signal.update, events = %+v", hub.events)
	}
	var sig struct {
		ID    string  `json:"id"`
		Freq  uint64  `json:"freqHz"`
		Mod   string  `json:"modulation"`
		Conf  float64 `json:"confidence"`
		Lat   float64 `json:"lat"`
		SDRID string  `json:"sdrId"`
	}
	if err := json.Unmarshal(hub.events[newIdx].Payload, &sig); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if sig.Freq != 146_520_000 || sig.Mod != "am" || sig.SDRID != "rtlsdr-0" {
		t.Fatalf("unexpected signal payload: %+v", sig)
	}
	if sig.Lat != 40.7 {
		t.Fatalf("lat = %v, want 40.7", sig.Lat)
	}
}

func TestPublishUnlocatedSDREmits(t *testing.T) {
	// A1 (§9.3): a signal from an SDR with no known location must still be
	// published (no map placement), with a NULL location rather than dropped.
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	p := newPublisher(nil, srv.URL, map[string]sdrLocation{}, time.Minute)
	p.publish(testEvent(), time.Now())

	if n := hub.waitCount(1); n < 1 {
		t.Fatalf("expected an event for the unlocated SDR, got %d", n)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.events[0].Type != "signal.new" {
		t.Fatalf("first event type = %q, want signal.new", hub.events[0].Type)
	}
	var sig struct {
		ID    string   `json:"id"`
		Lat   *float64 `json:"lat"`
		Lon   *float64 `json:"lon"`
		SDRID string   `json:"sdrId"`
	}
	if err := json.Unmarshal(hub.events[0].Payload, &sig); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if sig.Lat != nil || sig.Lon != nil {
		t.Fatalf("expected NULL lat/lon for unlocated SDR, got lat=%v lon=%v", sig.Lat, sig.Lon)
	}
	if sig.SDRID != "rtlsdr-0" {
		t.Fatalf("sdrId = %q, want rtlsdr-0", sig.SDRID)
	}
}

// TestPublishTracksMovement is the §9.4 two-point fixture: the
// "simulator" receiver relocates ~111 m north between two publishes;
// the track must report ≈ 11 km/h (moving) heading ≈ 0° (north) and
// emit a track.update event.
func TestPublishTracksMovement(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	locs := map[string]sdrLocation{"rtlsdr-0": {Lat: 40.0, Lon: -74.0}}
	p := newPublisher(nil, srv.URL, locs, time.Minute)

	now := time.Now()
	p.publish(testEvent(), now)

	// Move the receiver: +0.001° latitude ≈ 111 m north.
	p.mu.Lock()
	p.locations["rtlsdr-0"] = sdrLocation{Lat: 40.001, Lon: -74.0}
	p.mu.Unlock()
	p.publish(testEvent(), now.Add(36*time.Second))

	// 2 signal events + 2 track.update events (1 Hz throttle passes
	// for both publishes because they are 36 s apart).
	if n := hub.waitCount(4); n < 4 {
		t.Fatalf("expected >= 4 events (2 signal + 2 track), got %d", n)
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	var last trackUpdate
	found := false
	for _, ev := range hub.events {
		if ev.Type != "track.update" {
			continue
		}
		found = true
		if err := json.Unmarshal(ev.Payload, &last); err != nil {
			t.Fatalf("unmarshal track.update: %v", err)
		}
	}
	if !found {
		t.Fatalf("no track.update among %d events", len(hub.events))
	}
	if !last.IsMoving {
		t.Fatalf("isMoving = false, want true (payload %+v)", last)
	}
	if last.SpeedKmh < 10 || last.SpeedKmh > 12.5 {
		t.Fatalf("speedKmh = %v, want ~11.1 (111 m over 36 s)", last.SpeedKmh)
	}
	if last.HeadingDeg > 5 && last.HeadingDeg < 355 {
		t.Fatalf("headingDeg = %v, want ~0 (north)", last.HeadingDeg)
	}
	if last.Lat != 40.001 {
		t.Fatalf("lat = %v, want last fix 40.001", last.Lat)
	}
}

func TestSweepEmitsRemoved(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	locs := map[string]sdrLocation{"rtlsdr-0": {Lat: 40.7, Lon: -74.0}}
	p := newPublisher(nil, srv.URL, locs, 300*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.sweep(ctx)
	p.publish(testEvent(), time.Now())
	if n := hub.waitCount(1); n < 1 {
		t.Fatalf("expected 1 event, got %d", n)
	}

	// Wait for the sweeper to drop the idle signal (5s tick + 300ms ttl).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		n := len(hub.events)
		lastType := ""
		if n > 0 {
			lastType = hub.events[n-1].Type
		}
		hub.mu.Unlock()
		if n >= 2 && lastType == "signal.removed" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("expected signal.removed event after TTL expiry")
}

func TestObserveFrameEmitsSDRStatus(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	p := newPublisher(nil, srv.URL, map[string]sdrLocation{}, time.Minute)
	now := time.Now()
	p.observeFrame("rtlsdr-0", 146_520_000, now)
	if n := hub.waitCount(1); n < 1 {
		t.Fatal("expected a sdr.status event on first frame")
	}

	// Same frequency again → deduped, no new event.
	p.observeFrame("rtlsdr-0", 146_520_000, now.Add(100*time.Millisecond))
	if n := hub.waitCount(1); n != 1 {
		t.Fatalf("expected dedupe (1 event), got %d", n)
	}

	// Retune → new event with the new frequency.
	p.observeFrame("rtlsdr-0", 121_500_000, now.Add(2*time.Second))
	if n := hub.waitCount(2); n < 2 {
		t.Fatalf("expected status event after retune, got %d", n)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	last := hub.events[len(hub.events)-1]
	if last.Type != "sdr.status" {
		t.Fatalf("type = %q, want sdr.status", last.Type)
	}
	var st struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		FreqHz uint64 `json:"freqHz"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal(last.Payload, &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if st.ID != "rtlsdr-0" || st.FreqHz != 121_500_000 || !st.Active {
		t.Fatalf("unexpected sdr.status payload: %+v", st)
	}
}

func TestSDRSilenceDeactivates(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	p := newPublisher(nil, srv.URL, map[string]sdrLocation{}, 2*time.Second)
	p.initSDRs([]sdr.SDRCaptureConfig{{
		ID: "rtlsdr-0", Driver: "rtlsdr",
		DefaultFreq: 146_520_000, DefaultGain: 40, DefaultBW: 2_400_000,
	}})
	now := time.Now()
	p.observeFrame("rtlsdr-0", 146_520_000, now)
	if n := hub.waitCount(1); n < 1 {
		t.Fatal("expected initial sdr.status")
	}

	// Simulate silence far past the threshold (ttl/2, clamped ≥5s).
	p.mu.Lock()
	p.sdrs["rtlsdr-0"].lastFrame = now.Add(-time.Hour)
	p.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.sweep(ctx)

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		deactivated := false
		for _, ev := range hub.events {
			if ev.Type != "sdr.status" {
				continue
			}
			var st struct {
				ID     string `json:"id"`
				Active bool   `json:"active"`
			}
			if json.Unmarshal(ev.Payload, &st) == nil && st.ID == "rtlsdr-0" && !st.Active {
				deactivated = true
			}
		}
		hub.mu.Unlock()
		if deactivated {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("expected sdr.status deactivation after silence")
}

func TestResolveClassification(t *testing.T) {
	onnx := &classify.Result{Modulation: "CW", Confidence: 0.9, Method: "onnx", Bandwidth: 0, Source: "wrong"}
	rules := &classify.Result{Modulation: "AM", Source: "aviation", Confidence: 0.7, Method: "rules", Bandwidth: 12_000}

	// Above threshold: ONNX wins, source comes from rules, bandwidth
	// from the frame measurement.
	got := resolveClassification(onnx, rules, 25_000, 0.5)
	if got != onnx {
		t.Fatalf("expected the onnx result to be returned in place, got %+v", got)
	}
	if got.Source != "aviation" || got.Bandwidth != 25_000 || got.Method != "onnx" {
		t.Fatalf("merged result = %+v, want source=aviation bandwidth=25000 method=onnx", got)
	}

	// Below threshold: rules fallback (method stays "rules", §16.1).
	low := &classify.Result{Modulation: "CW", Confidence: 0.3, Method: "onnx"}
	got = resolveClassification(low, rules, 25_000, 0.5)
	if got != rules {
		t.Fatalf("expected rules fallback below min_confidence, got %+v", got)
	}
	if got.Method != "rules" {
		t.Errorf("fallback method = %q, want rules", got.Method)
	}
}

func TestQueueDropWhenFull(t *testing.T) {
	// Silence expected drop noise.
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	p := newPublisher(nil, "http://127.0.0.1:1", map[string]sdrLocation{}, time.Minute)
	// The worker drains to an unreachable endpoint (fast connection
	// refused); this only checks the non-blocking drop behavior.
	for i := 0; i < 300; i++ {
		p.queue("signal.update", map[string]string{"i": "x"})
	}
	// Must not block or panic.
}

func TestProcessFrameBelowCenterOffset(t *testing.T) {
	// D4 (§5.3): a tone BELOW the tuning center is detected and reported at
	// center + (negative offset). Before the fix the negative offset overflowed
	// the uint64 add and the signal got a garbage frequency (or was missed).
	pd := &dsp.PeakDetector{ThresholdDB: -60, MinSpacing: 10, TopN: 20}
	rules := classify.NewRuleClassifier()
	const (
		center = uint64(10_000_000)
		offset = -2_000_000.0 // below center, within (-fs/2, 0) at 4.096 Msps
	)
	events := processFrame(pipelineFrame(t, center, offset, 0), pd, rules, dsp.WindowRectangular, nil)
	if len(events) == 0 {
		t.Fatal("no events for below-center tone")
	}
	// events are sorted strongest-first; the tone is the strongest peak.
	got := int64(events[0].PeakHz)
	want := int64(center) + int64(offset)
	if math.Abs(float64(got-want)) > 2000 {
		t.Fatalf("below-center peak = %d Hz, want ~%d Hz", got, want)
	}
	if events[0].OffsetHz >= 0 {
		t.Errorf("OffsetHz = %v, want negative (below center)", events[0].OffsetHz)
	}
}

func TestProcessFrameClampsBelowZero(t *testing.T) {
	// D4 (§5.3): when center + negative offset < 0, the absolute frequency is
	// clamped to 0 rather than wrapping to a huge value.
	pd := &dsp.PeakDetector{ThresholdDB: -60, MinSpacing: 10, TopN: 20}
	rules := classify.NewRuleClassifier()
	const (
		center = uint64(500_000)
		offset = -2_000_000.0 // center+offset < 0 -> must clamp to 0
	)
	events := processFrame(pipelineFrame(t, center, offset, 0), pd, rules, dsp.WindowRectangular, nil)
	if len(events) == 0 {
		t.Fatal("no events")
	}
	if events[0].PeakHz != 0 {
		t.Fatalf("PeakHz = %d, want 0 (clamped)", events[0].PeakHz)
	}
}

// B4: the peak detector's SNR gate — a candidate must clear the
// record's own noise floor (median of the lower half of bins) by
// MinSNRDB, no matter where the absolute threshold sits. Before the
// gate, threshold_db (-60) sat below the noise floor's absolute
// dBFS level and every noise bump published as a signal.
func TestPeakDetectorSnrGate(t *testing.T) {
	spec := &dsp.FFTResult{
		SampleRate:  256_000,
		BinSpacing:  1000,
		PowerDB:     make([]float64, 256),
		Frequencies: make([]float64, 256),
	}
	for i := range spec.PowerDB {
		spec.PowerDB[i] = -70 // flat noise floor
		spec.Frequencies[i] = float64(i) * 1000
	}
	spec.PowerDB[100] = -55 // carrier: 15 dB over the floor
	spec.PowerDB[140] = -65 // noise bump: only 5 dB over the floor,
	// but comfortably above the absolute threshold below.

	gated := &dsp.PeakDetector{ThresholdDB: -80, MinSNRDB: 10, MinSpacing: 20, TopN: 20}
	peaks := gated.Detect(spec)
	if len(peaks) != 1 || peaks[0].Index != 100 {
		t.Fatalf("SNR gate: want only the bin-100 carrier, got %+v", peaks)
	}

	// MinSNRDB == 0 disables the gate: both peaks survive, matching
	// pre-B4 behavior.
	ungated := &dsp.PeakDetector{ThresholdDB: -80, MinSpacing: 20, TopN: 20}
	peaks = ungated.Detect(spec)
	if len(peaks) != 2 || peaks[0].Index != 100 || peaks[1].Index != 140 {
		t.Fatalf("gate disabled: want bins 100 and 140, got %+v", peaks)
	}
}

// §5.7: the assembled wire path — four 1024-pair frames concatenated
// by dsp.FFTAssembler — must produce the same events as one
// 4096-pair frame carrying the same samples (same peak, same power:
// the FFT runs once on identical bytes).
func TestAssembledFramesMatchSingleFrame(t *testing.T) {
	pd := &dsp.PeakDetector{ThresholdDB: -60, MinSpacing: 10, TopN: 20}
	rules := classify.NewRuleClassifier()
	const (
		center = uint64(10_000_000)
		rate   = 4_096_000.0
	)
	build := func(frameIdx int) []int16 {
		samples := make([]int16, 2*1024)
		for i := 0; i < 1024; i++ {
			n := i + frameIdx*1024
			ph := 2 * math.Pi * float64(n) / 4 // one cycle per 4 pairs
			samples[2*i] = int16(8000 * math.Cos(ph))
			samples[2*i+1] = int16(8000 * math.Sin(ph))
		}
		return samples
	}
	single := &sdr.IQFrame{
		SDRID: "sdr0", FreqHz: center, SampleRate: uint32(rate),
	}
	for f := 0; f < 4; f++ {
		single.Samples = append(single.Samples, build(f)...)
	}
	want := processFrame(single, pd, rules, dsp.WindowRectangular, nil)
	if len(want) == 0 {
		t.Fatal("no events from single 4096-pair frame")
	}

	var got []signalEvent
	asm := dsp.NewFFTAssembler(4096)
	for f := 0; f < 4; f++ {
		fr := &sdr.IQFrame{
			SDRID: "sdr0", FreqHz: center, SampleRate: uint32(rate),
			Samples: build(f),
		}
		asm.Offer(fr.SDRID, fr.FreqHz, fr.Samples, func(buf []int16) {
			m := *fr
			m.Samples = buf
			got = processFrame(&m, pd, rules, dsp.WindowRectangular, nil)
		})
	}
	if len(got) == 0 {
		t.Fatal("no events from assembled 4x1024-pair path")
	}
	if got[0].PeakHz != want[0].PeakHz {
		t.Fatalf("PeakHz = %d, want %d", got[0].PeakHz, want[0].PeakHz)
	}
	if got[0].PowerDB != want[0].PowerDB {
		t.Fatalf("PowerDB = %v, want %v (identical bytes must give "+
			"identical power)", got[0].PowerDB, want[0].PowerDB)
	}
}

func TestPublishVerifiesPair(t *testing.T) {
	// §8: the same carrier seen by two SDRs inside the time window
	// latches verified on both rows and re-broadcasts full updates.
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	p := newPublisher(nil, srv.URL, map[string]sdrLocation{}, time.Minute)

	now := time.Now()
	evA := testEvent() // SDRID rtlsdr-0 @ 146.52 MHz
	evB := testEvent()
	evB.SDRID = "rtlsdr-1"
	p.publish(evA, now)
	p.publish(evB, now.Add(100*time.Millisecond))

	idA := db.SignalID("rtlsdr-0", 146_520_000)
	idB := db.SignalID("rtlsdr-1", 146_520_000)
	p.mu.Lock()
	va, vb := p.verified[idA], p.verified[idB]
	p.mu.Unlock()
	if !va || !vb {
		t.Fatalf("pair not verified: idA=%v idB=%v", va, vb)
	}

	// signal.new ×2 from the publishes + signal.update ×2 from the
	// verified pair.
	if n := hub.waitCount(4); n < 4 {
		t.Fatalf("expected ≥4 events, got %d", n)
	}

	// A re-observation keeps the latch: its payload is already verified
	// and the pair tracker must not verify again.
	p.publish(testEvent(), now.Add(300*time.Millisecond))
	if n := hub.waitCount(5); n < 5 {
		t.Fatalf("expected ≥5 events after re-observation, got %d", n)
	}
	time.Sleep(100 * time.Millisecond) // let any stray events land

	hub.mu.Lock()
	defer hub.mu.Unlock()
	verifiedUpdates := 0
	for _, ev := range hub.events {
		if ev.Type != "signal.update" {
			continue
		}
		var sig db.Signal
		if err := json.Unmarshal(ev.Payload, &sig); err != nil {
			t.Fatalf("unmarshal signal.update payload: %v", err)
		}
		if !sig.Verified {
			continue
		}
		if sig.ID != idA && sig.ID != idB {
			t.Errorf("verified update for unexpected id %s", sig.ID)
			continue
		}
		verifiedUpdates++
		if sig.FreqHz != 146_520_000 {
			t.Errorf("verified payload freq = %d, want 146520000 (full payload required)", sig.FreqHz)
		}
	}
	// Two from verifyPair + one from the latched re-observation.
	if verifiedUpdates != 3 {
		t.Fatalf("verified signal.update events = %d, want 3", verifiedUpdates)
	}
}

func TestPublishNoVerifyOnFreqMismatch(t *testing.T) {
	// §8.2: peaks more than 5 kHz apart are different signals and must
	// never cross-verify.
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	p := newPublisher(nil, srv.URL, map[string]sdrLocation{}, time.Minute)

	now := time.Now()
	evA := testEvent()
	evB := testEvent()
	evB.SDRID = "rtlsdr-1"
	evB.PeakHz = 146_530_000 // 10 kHz away
	p.publish(evA, now)
	p.publish(evB, now.Add(100*time.Millisecond))

	time.Sleep(150 * time.Millisecond)

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.verified) != 0 {
		t.Errorf("unexpected verifications: %v", p.verified)
	}
}

// §5.6: calibrate decorates events from SDRs with a calibration_offset_db
// (power_dbm = power_db − applied_gain + offset) and is a no-op for
// uncalibrated or unknown SDRs. Gain polling (updateGain) must influence
// the result so runtime POST /api/v1/gain changes stay honest.
func TestCalibrateSignalPower(t *testing.T) {
	off := -12.5
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	p := newPublisher(nil, srv.URL, map[string]sdrLocation{}, 2*time.Second)
	p.initSDRs([]sdr.SDRCaptureConfig{
		{ID: "cal-0", Driver: "rtlsdr", DefaultFreq: 100_000_000,
			DefaultGain: 40, DefaultBW: 2_400_000, CalibrationOffsetDB: &off},
		{ID: "raw-0", Driver: "rtlsdr", DefaultFreq: 100_000_000,
			DefaultGain: 40, DefaultBW: 2_400_000},
	})

	ev := signalEvent{SDRID: "cal-0", PowerDB: -50}
	p.calibrate(&ev)
	if !ev.Calibrated {
		t.Fatal("expected Calibrated=true for an SDR with calibration_offset_db")
	}
	if ev.PowerDBM != -102.5 { // −50 − 40 − 12.5
		t.Fatalf("PowerDBM = %v, want −102.5", ev.PowerDBM)
	}

	// Uncalibrated SDR: pass-through (relative dB), flag stays false.
	raw := signalEvent{SDRID: "raw-0", PowerDB: -50}
	p.calibrate(&raw)
	if raw.Calibrated || raw.PowerDBM != 0 {
		t.Fatalf("uncalibrated event was mutated: %+v", raw)
	}

	// Unknown SDR (frame from an unconfigured device): no-op.
	ghost := signalEvent{SDRID: "ghost", PowerDB: -50}
	p.calibrate(&ghost)
	if ghost.Calibrated {
		t.Fatal("unknown SDR must not be calibrated")
	}

	// A polled gain change must flow into the calibration math (§5.6).
	p.updateGain("cal-0", 30)
	ev2 := signalEvent{SDRID: "cal-0", PowerDB: -50}
	p.calibrate(&ev2)
	if !ev2.Calibrated || ev2.PowerDBM != -92.5 { // −50 − 30 − 12.5
		t.Fatalf("after gain change PowerDBM = %v Calibrated=%v, want −92.5 true",
			ev2.PowerDBM, ev2.Calibrated)
	}

	// updateGain on unknown SDRs is a silent no-op.
	p.updateGain("ghost", 99)
}

// §5.6: pollGainOnce reads a capture control status snapshot (array of
// slot maps with id/gain_db) and refreshes state; unknown IDs are ignored.
func TestPollGainOnce(t *testing.T) {
	hub := &fakeHub{}
	srv := hub.serve()
	defer srv.Close()

	status := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"cal-0","gain_db":21.4},{"id":"other","gain_db":10}]`)
	}))
	defer status.Close()

	p := newPublisher(nil, srv.URL, map[string]sdrLocation{}, 2*time.Second)
	p.initSDRs([]sdr.SDRCaptureConfig{
		{ID: "cal-0", Driver: "rtlsdr", DefaultFreq: 100_000_000,
			DefaultGain: 40, DefaultBW: 2_400_000},
	})
	p.pollGainOnce(status.URL + "/api/v1/status")

	p.mu.Lock()
	got := p.sdrs["cal-0"].gainDB
	p.mu.Unlock()
	if got != 21.4 {
		t.Fatalf("gainDB = %v, want 21.4 after poll", got)
	}
	// An unreachable endpoint must not panic or wedge the poller.
	p.pollGainOnce("http://127.0.0.1:1/api/v1/status")
}
