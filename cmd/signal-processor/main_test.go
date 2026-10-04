package main

import (
	"context"
	"encoding/json"
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
	if hub.events[0].Type != "signal.new" {
		t.Fatalf("first event type = %q, want signal.new", hub.events[0].Type)
	}
	if hub.events[1].Type != "signal.update" {
		t.Fatalf("second event type = %q, want signal.update", hub.events[1].Type)
	}
	var sig struct {
		ID    string  `json:"id"`
		Freq  uint64  `json:"freqHz"`
		Mod   string  `json:"modulation"`
		Conf  float64 `json:"confidence"`
		Lat   float64 `json:"lat"`
		SDRID string  `json:"sdrId"`
	}
	if err := json.Unmarshal(hub.events[0].Payload, &sig); err != nil {
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
	events := processFrame(pipelineFrame(t, center, offset, 0), pd, rules)
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
	events := processFrame(pipelineFrame(t, center, offset, 0), pd, rules)
	if len(events) == 0 {
		t.Fatal("no events")
	}
	if events[0].PeakHz != 0 {
		t.Fatalf("PeakHz = %d, want 0 (clamped)", events[0].PeakHz)
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
