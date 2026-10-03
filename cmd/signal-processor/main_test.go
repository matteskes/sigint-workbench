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
	"sigint-workbench/internal/dsp"
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