// signal-processor - FFT, peak detection, band identification, classification.
//
// Receives IQ frames over UDP from iq-ingest, runs the DSP pipeline:
//
//	IQ int16 -> float64 -> IQ FFT -> peak detect -> band ID -> classify
//
// Detected signals are logged to stdout in a structured format,
// upserted to the PostGIS database (when DB_URL is set), and
// broadcast as real-time events to ws-hub (when WS_HUB_URL is set).
//
// Run:
//
//	./bin/signal-processor -port 9010
//
// Or with env vars (Docker):
//
//	LISTEN_PORT=9010 DB_URL=... WS_HUB_URL=http://ws-hub:8081 MODEL_PATH=/models/classifier.onnx ./bin/signal-processor
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"sigint-workbench/internal/classify"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
	"sigint-workbench/internal/ws"
)

// signalEvent is a classified signal detected in the spectrum.
type signalEvent struct {
	SDRID      string             `json:"sdr_id"`
	Timestamp  time.Time          `json:"timestamp"`
	CenterHz   uint64             `json:"center_hz"`
	PeakHz     uint64             `json:"peak_hz"`
	OffsetHz   float64            `json:"offset_hz"`
	PowerDB    float64            `json:"power_db"`
	Bandwidth  float64            `json:"bandwidth_hz"`
	BandName   string             `json:"band_name"`
	Class      *classify.Result   `json:"class"`
}

// sdrLocation is a receiver's physical location.
type sdrLocation struct {
	Lat float64
	Lon float64
}

// publisher persists detected signals to the database and emits
// real-time events. All methods are safe for concurrent use.
type publisher struct {
	db        *db.DB
	client    *http.Client
	wsHubURL  string
	locations map[string]sdrLocation
	ttl       time.Duration

	events chan ws.Event // async queue of events for ws-hub

	mu        sync.Mutex
	seen      map[string]time.Time // last activity per published signal ID
	firstSeen map[string]time.Time
}

// newPublisher creates a publisher. db, wsHubURL and locations may be
// nil/empty — the publisher degrades gracefully to log-only mode.
func newPublisher(database *db.DB, wsHubURL string, locations map[string]sdrLocation, ttl time.Duration) *publisher {
	p := &publisher{
		db:        database,
		client:    &http.Client{Timeout: 2 * time.Second},
		wsHubURL:  wsHubURL,
		locations: locations,
		ttl:       ttl,
		events:    make(chan ws.Event, 256),
		seen:      make(map[string]time.Time),
		firstSeen: make(map[string]time.Time),
	}
	if wsHubURL != "" {
		go p.runEventWorker()
	}
	return p
}

// publish records one detected signal. It upserts to the database and
// emits a signal.new/signal.update event when the detecting SDR has a
// known location; otherwise the signal is ignored (no map placement).
func (p *publisher) publish(ev signalEvent, now time.Time) {
	loc, ok := p.locations[ev.SDRID]
	if !ok {
		return
	}
	id := db.SignalID(ev.SDRID, ev.PeakHz)

	p.mu.Lock()
	isNew := false
	if _, ok := p.seen[id]; !ok {
		isNew = true
		p.firstSeen[id] = now
	}
	p.seen[id] = now
	first := p.firstSeen[id]
	p.mu.Unlock()

	mod := ""
	subType := ""
	class := ""
	conf := 0.0
	bw := int32(0)
	if ev.Class != nil {
		mod = ev.Class.Modulation
		subType = ev.Class.SubType
		class = ev.Class.Source
		conf = ev.Class.Confidence
		bw = int32(ev.Bandwidth)
	}
	sig := &db.Signal{
		ID:          id,
		FreqHz:      ev.PeakHz,
		BandwidthHz: bw,
		Modulation:  mod,
		SubType:     subType,
		Class:       class,
		Confidence:  conf,
		PowerDBM:    ev.PowerDB,
		Lat:         loc.Lat,
		Lon:         loc.Lon,
		FirstSeen:   first,
		LastSeen:    now,
		SDRID:       ev.SDRID,
	}

	if p.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := p.db.UpsertSignal(ctx, sig)
		cancel()
		if err != nil {
			log.Printf("upsert signal %s: %v", id, err)
		}
	}

	typ := "signal.update"
	if isNew {
		typ = "signal.new"
	}
	p.queue(typ, sig)
}

// sweep removes signals that have been idle longer than the TTL,
// deleting their database rows and emitting signal.removed events.
func (p *publisher) sweep(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			var removed []string
			p.mu.Lock()
			for id, last := range p.seen {
				if now.Sub(last) > p.ttl {
					removed = append(removed, id)
					delete(p.seen, id)
					delete(p.firstSeen, id)
				}
			}
			p.mu.Unlock()

			for _, id := range removed {
				if p.db != nil {
					cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					if err := p.db.DeleteSignal(cctx, id); err != nil {
						log.Printf("delete signal %s: %v", id, err)
					}
					cancel()
				}
				p.queue("signal.removed", map[string]string{"id": id})
			}
		}
	}
}

// queue enqueues an event for delivery to ws-hub (non-blocking).
func (p *publisher) queue(typ string, payload interface{}) {
	if p.wsHubURL == "" {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Printf("marshal %s payload: %v", typ, err)
		return
	}
	select {
	case p.events <- ws.Event{Type: typ, Payload: raw}:
	default:
		log.Printf("ws event queue full, dropping %s", typ)
	}
}

// runEventWorker delivers queued events to the ws-hub ingest endpoint.
func (p *publisher) runEventWorker() {
	for ev := range p.events {
		body, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		resp, err := p.client.Post(p.wsHubURL+"/api/events", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("ws-hub: %v", err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf("ws-hub: ingest returned %d", resp.StatusCode)
		}
	}
}

// loadLocations reads SDR lat/lon from the sdr-capture config file.
func loadLocations(path string) map[string]sdrLocation {
	locs := make(map[string]sdrLocation)
	cfg, err := sdr.LoadCaptureConfig(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("SDR config %s not found, signals will not be geo-located", path)
		} else {
			log.Printf("load SDR config %s: %v", path, err)
		}
		return locs
	}
	for _, s := range cfg.SDRs {
		if s.Lat != nil && s.Lon != nil {
			locs[s.ID] = sdrLocation{Lat: *s.Lat, Lon: *s.Lon}
		}
	}
	return locs
}

// frameClassifier classifies one detected peak. RuleClassifier is the
// default; onnxFrameClassifier wraps the ML model with a per-peak
// rules fallback so a bad frame never costs a detection.
type frameClassifier interface {
	Classify(freqHz uint64, bandwidthHz float64, spectrum *dsp.FFTResult) *classify.Result
}

// onnxFrameClassifier runs the ONNX model on the peak's extracted
// features; any failure (or missing features) falls back to rules.
type onnxFrameClassifier struct {
	onnx  *classify.ONNXClassifier
	rules *classify.RuleClassifier
}

func (c *onnxFrameClassifier) Classify(freqHz uint64, bandwidthHz float64, spectrum *dsp.FFTResult) *classify.Result {
	if f := classify.ExtractFeatures(spectrum, freqHz); f != nil {
		if res, err := c.onnx.Classify(f.ToVector(), freqHz); err == nil && res != nil {
			res.Bandwidth = bandwidthHz
			return res
		}
	}
	return c.rules.Classify(freqHz, bandwidthHz, spectrum)
}

func main() {
	listenPort := flag.Int("port", envInt("LISTEN_PORT", 9010), "UDP listen port")
	thresholdDB := flag.Float64("threshold", -60, "peak threshold dB")
	maxPeaks := flag.Int("max-peaks", 20, "max peaks per frame")
	configPath := flag.String("config", envStr("SDR_CONFIG", "config/sdr-capture.yaml"), "sdr-capture config (SDR locations)")
	modelPath := flag.String("model", envStr("MODEL_PATH", ""), "ONNX classifier model path (empty = rules only)")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("signal-processor: ")

	// DSP components
	peakDetector := &dsp.PeakDetector{
		ThresholdDB: *thresholdDB,
		MinSpacing:  10,
		TopN:        *maxPeaks,
	}
	ruleClassifier := classify.NewRuleClassifier()
	var classifier frameClassifier = ruleClassifier
	if *modelPath != "" {
		onnxClassifier := classify.NewONNXClassifier(*modelPath)
		if err := onnxClassifier.Load(); err != nil {
			log.Printf("ONNX classifier unavailable (%v); using rules", err)
		} else {
			defer onnxClassifier.Close()
			classifier = &onnxFrameClassifier{onnx: onnxClassifier, rules: ruleClassifier}
			log.Printf("loaded ONNX classifier %s", *modelPath)
		}
	}

	// Optional database
	var database *db.DB
	if dbURL := os.Getenv("DB_URL"); dbURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var err error
		database, err = db.New(ctx, dbURL)
		cancel()
		if err != nil {
			log.Printf("database not available (%v), continuing without", err)
			database = nil
		} else {
			defer database.Close()
			log.Printf("connected to database")
		}
	}

	// Publisher (DB + ws-hub + SDR locations)
	wsHubURL := os.Getenv("WS_HUB_URL")
	ttl := time.Duration(envInt("SIGNAL_TTL", 30)) * time.Second
	pub := newPublisher(database, wsHubURL, loadLocations(*configPath), ttl)
	if wsHubURL != "" {
		log.Printf("emitting events to %s (ttl=%v)", wsHubURL, ttl)
	}

	// UDP listener
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", *listenPort))
	if err != nil {
		log.Fatalf("resolve: %v", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer conn.Close()
	log.Printf("listening on UDP :%d  threshold=%.0f dB  max_peaks=%d",
		*listenPort, *thresholdDB, *maxPeaks)

	// Buffer
	buf := make([]byte, sdr.IQHeaderSize+sdr.MaxIQSamplesPerFrame*4)

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sig := <-sigCh
		log.Printf("shutting down (%v)", sig)
		cancel()
		conn.Close()
		time.Sleep(50 * time.Millisecond)
		os.Exit(0)
	}()
	go pub.sweep(ctx)

	// Throttle: log + emit at most once per 2 seconds per SDR+freq
	lastEmit := make(map[string]time.Time)

	// Main loop
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-sigCh:
				return
			default:
				log.Printf("read: %v", err)
				time.Sleep(50 * time.Millisecond)
				continue
			}
		}

		frame, err := sdr.DecodeIQFrame(buf[:n])
		if err != nil {
			continue
		}

		events := processFrame(frame, peakDetector, classifier)

		// Log + publish events (throttled)
		now := time.Now()
		for _, ev := range events {
			key := fmt.Sprintf("%s:%d", ev.SDRID, ev.PeakHz/1000) // per SDR+freq (kHz)
			if last, ok := lastEmit[key]; ok && now.Sub(last) < 2*time.Second {
				continue
			}
			lastEmit[key] = now
			logSignal(ev)
			pub.publish(ev, now)
		}
	}
}

// processFrame runs the DSP pipeline on one IQ frame.
func processFrame(frame *sdr.IQFrame, pd *dsp.PeakDetector, classifier frameClassifier) []signalEvent {
	pairs := len(frame.Samples) / 2
	if pairs < 64 {
		return nil
	}

	// Convert int16 IQ to float64 (normalized to -1.0 .. 1.0)
	iqFloat := make([]float64, len(frame.Samples))
	for i, v := range frame.Samples {
		iqFloat[i] = float64(v) / 32768.0
	}

	// IQ FFT
	result, err := dsp.ComputeIQFFT(iqFloat, frame.SampleRate)
	if err != nil || result == nil {
		return nil
	}

	// Peak detection
	peaks := pd.Detect(result)
	if len(peaks) == 0 {
		return nil
	}

	// Noise floor for SNR
	noiseFloor := dsp.DetectNoiseFloor(result.PowerDB)

	// Build events
	var events []signalEvent
	for _, p := range peaks {
		// Map FFT bin frequency to absolute frequency
		// IQ FFT: bin freq is offset from center (0 to fs/2)
		offsetHz := p.FreqHz
		peakHz := frame.FreqHz + uint64(offsetHz)

		// Band identification
		band := dsp.IdentifyBand(peakHz)
		bandName := "Unknown"
		if band != nil {
			bandName = band.Name
		}

		// Classification
		result := classifier.Classify(peakHz, p.Bandwidth, result)

		events = append(events, signalEvent{
			SDRID:     frame.SDRID,
			Timestamp: frame.Timestamp,
			CenterHz:  frame.FreqHz,
			PeakHz:    peakHz,
			OffsetHz:  offsetHz,
			PowerDB:   p.PowerDB,
			Bandwidth: p.Bandwidth,
			BandName:  bandName,
			Class:     result,
		})
	}

	// Sort by power (strongest first)
	sort.Slice(events, func(i, j int) bool {
		return events[i].PowerDB > events[j].PowerDB
	})

	_ = noiseFloor // available for future SNR-based filtering
	return events
}

// logSignal prints a classified signal to stdout.
func logSignal(ev signalEvent) {
	mod := "?"
	src := "?"
	conf := 0.0
	bw := ev.Bandwidth
	if ev.Class != nil {
		mod = ev.Class.Modulation
		src = ev.Class.Source
		conf = ev.Class.Confidence
		bw = ev.Class.Bandwidth
	}
	log.Printf("SIGNAL  %s  %.4f MHz  %s  %s/%s  %.1f dB  BW:%.0f kHz  conf:%.2f",
		ev.SDRID,
		float64(ev.PeakHz)/1e6,
		ev.BandName,
		mod, src,
		ev.PowerDB,
		bw/1000,
		conf,
	)

	// Also emit JSON for programmatic consumption
	out, _ := json.Marshal(ev)
	log.Printf("JSON    %s", string(out))
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}