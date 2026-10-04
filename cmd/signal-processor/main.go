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
	"sigint-workbench/internal/config"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/location"
	"sigint-workbench/internal/sdr"
	"sigint-workbench/internal/ws"
)

// signalEvent is a classified signal detected in the spectrum.
type signalEvent struct {
	SDRID     string           `json:"sdr_id"`
	Timestamp time.Time        `json:"timestamp"`
	CenterHz  uint64           `json:"center_hz"`
	PeakHz    uint64           `json:"peak_hz"`
	OffsetHz  float64          `json:"offset_hz"`
	PowerDB   float64          `json:"power_db"`
	Bandwidth float64          `json:"bandwidth_hz"`
	BandName  string           `json:"band_name"`
	Class     *classify.Result `json:"class"`
}

// sdrLocation is a receiver's physical location.
type sdrLocation struct {
	Lat float64
	Lon float64
}

// sdrState tracks one known SDR for sdr.status emission (§14.4.3).
type sdrState struct {
	configured bool // seeded from the sdr-capture config
	model      string
	serial     string
	gainDB     float64
	bwHz       uint32
	lat, lon   *float64
	freqHz     uint64
	active     bool
	lastFrame  time.Time
	emittedAt  time.Time
}

// statusEvent builds the sdr.status payload: the §12.1 SDRDevice
// fields plus bwHz, camelCase per §12.7.
func (s *sdrState) statusEvent(id string) sdrStatusEvent {
	return sdrStatusEvent{
		ID:     id,
		Model:  s.model,
		FreqHz: s.freqHz,
		GainDB: s.gainDB,
		BWHz:   s.bwHz,
		Active: s.active,
		Lat:    s.lat,
		Lon:    s.lon,
	}
}

// sdrStatusEvent is the sdr.status payload (§3.2/§14.4.3).
type sdrStatusEvent struct {
	ID     string   `json:"id"`
	Model  string   `json:"model"`
	FreqHz uint64   `json:"freqHz"`
	GainDB float64  `json:"gainDb"`
	BWHz   uint32   `json:"bwHz"`
	Active bool     `json:"active"`
	Lat    *float64 `json:"lat,omitempty"`
	Lon    *float64 `json:"lon,omitempty"`
}

// driverModel maps a capture-config driver to a display model name.
func driverModel(driver string) string {
	switch driver {
	case "rtlsdr":
		return "RTL2832U"
	case "hackrf":
		return "HackRF One"
	case "simulator":
		return "Simulator"
	}
	return driver
}

// publisher persists detected signals to the database and emits
// real-time events. All methods are safe for concurrent use.
type publisher struct {
	db        *db.DB
	client    *http.Client
	wsHubURL  string
	locations map[string]sdrLocation
	ttl       time.Duration

	sdrSilenceTTL time.Duration        // SDR idle threshold (§14.4.3)
	sdrs          map[string]*sdrState // known SDRs → status state

	events chan ws.Event // async queue of events for ws-hub

	tracker *location.PairTracker // §8 two-SDR verification

	mu        sync.Mutex
	seen      map[string]time.Time // last activity per published signal ID
	firstSeen map[string]time.Time
	verified  map[string]bool       // signal IDs already verified (§8 latch)
	lastSig   map[string]*db.Signal // last published payload per signal ID
}

// newPublisher creates a publisher. db, wsHubURL and locations may be
// nil/empty — the publisher degrades gracefully to log-only mode.
func newPublisher(database *db.DB, wsHubURL string, locations map[string]sdrLocation, ttl time.Duration) *publisher {
	silence := ttl / 2
	if silence < 5*time.Second {
		silence = 5 * time.Second
	}
	if silence > 30*time.Second {
		silence = 30 * time.Second
	}
	p := &publisher{
		db:            database,
		client:        &http.Client{Timeout: 2 * time.Second},
		wsHubURL:      wsHubURL,
		locations:     locations,
		ttl:           ttl,
		sdrSilenceTTL: silence,
		sdrs:          make(map[string]*sdrState),
		events:        make(chan ws.Event, 256),
		seen:          make(map[string]time.Time),
		firstSeen:     make(map[string]time.Time),
		verified:      make(map[string]bool),
		lastSig:       make(map[string]*db.Signal),
		tracker:       location.NewPairTracker(location.NewVerifier()),
	}
	if wsHubURL != "" {
		go p.runEventWorker()
	}
	return p
}

// initSDRs seeds publisher SDR state from the sdr-capture config so
// sdr.status events carry model/gain/bandwidth and the sweeper knows
// which devices to watch (§14.4.3).
func (p *publisher) initSDRs(cfgs []sdr.SDRCaptureConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range cfgs {
		st := &sdrState{
			configured: true,
			model:      driverModel(c.Driver),
			serial:     c.Serial,
			gainDB:     c.DefaultGain,
			bwHz:       c.DefaultBW,
		}
		if c.Lat != nil && c.Lon != nil {
			st.lat, st.lon = c.Lat, c.Lon
		}
		p.sdrs[c.ID] = st
	}
}

// observeFrame records one received frame from an SDR and emits a
// deduplicated sdr.status when the device's effective state changes —
// first frame, retune, or reactivation after silence (§14.4.3).
func (p *publisher) observeFrame(sdrID string, freqHz uint64, now time.Time) {
	p.mu.Lock()
	st, ok := p.sdrs[sdrID]
	if !ok {
		st = &sdrState{model: "unknown"}
		p.sdrs[sdrID] = st
	}
	firstFrame := st.lastFrame.IsZero()
	reactivated := !st.active
	freqChanged := st.freqHz != freqHz
	st.lastFrame = now
	st.active = true
	st.freqHz = freqHz
	if !firstFrame && !reactivated && !freqChanged {
		p.mu.Unlock()
		return // deduped: effective state unchanged
	}
	// Throttle repeats (e.g. rapid retunes): ≥1 s between events,
	// except for the first frame and reactivations which always emit.
	if !st.emittedAt.IsZero() && now.Sub(st.emittedAt) < time.Second && !firstFrame && !reactivated {
		p.mu.Unlock()
		return
	}
	st.emittedAt = now
	ev := st.statusEvent(sdrID)
	configured := st.configured
	serial := st.serial
	lat, lon := st.lat, st.lon
	p.mu.Unlock()

	p.queue("sdr.status", ev)
	if p.db != nil && configured {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sdev := &db.SDRDevice{
			ID:     sdrID,
			Model:  ev.Model,
			Serial: serial,
			GainDB: ev.GainDB,
			FreqHz: freqHz,
			Active: true,
		}
		if lat != nil {
			sdev.Lat = *lat
		}
		if lon != nil {
			sdev.Lon = *lon
		}
		if err := p.db.UpsertSDR(ctx, sdev); err != nil {
			log.Printf("upsert sdr %s: %v", sdrID, err)
		}
	}
}

// publish records one detected signal. It always upserts to the database
// and emits a signal.new/signal.update event (§9.3). When the detecting
// SDR has a known location it is attached to the row; otherwise the signal
// is persisted unlocated (lat/lon NULL) and the map layer omits it.
func (p *publisher) publish(ev signalEvent, now time.Time) {
	// A1 (§9.3): unlocated SDRs are still tracked. loc stays nil so the
	// signal is persisted with a NULL location instead of being dropped.
	var loc *sdrLocation
	if l, ok := p.locations[ev.SDRID]; ok {
		loc = &l
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
	alreadyVerified := p.verified[id] // §8: once verified, stays verified
	p.mu.Unlock()

	mod := ""
	subType := ""
	class := ""
	method := ""
	conf := 0.0
	bw := int32(0)
	if ev.Class != nil {
		mod = ev.Class.Modulation
		subType = ev.Class.SubType
		class = ev.Class.Source
		method = ev.Class.Method
		conf = ev.Class.Confidence
		bw = int32(ev.Bandwidth)
	}
	// Unlocated SDR -> NULL lat/lon (A1, §9.3).
	var lat, lon *float64
	if loc != nil {
		lat, lon = &loc.Lat, &loc.Lon
	}
	sig := &db.Signal{
		ID:          id,
		FreqHz:      ev.PeakHz,
		BandwidthHz: bw,
		Modulation:  mod,
		SubType:     subType,
		Class:       class,
		Method:      method,
		Confidence:  conf,
		PowerDBM:    ev.PowerDB,
		Lat:         lat,
		Lon:         lon,
		FirstSeen:   first,
		LastSeen:    now,
		SDRID:       ev.SDRID,
		Verified:    alreadyVerified,
		Active:      true,
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

	// §8: remember the payload so a later verified pair can re-broadcast
	// full fields, then run the two-SDR pair tracker on this observation.
	p.mu.Lock()
	p.lastSig[id] = sig
	p.mu.Unlock()
	for _, r := range p.tracker.Observe(location.Observation{
		SDRID:    ev.SDRID,
		SignalID: id,
		FreqHz:   ev.PeakHz,
		PowerDB:  ev.PowerDB,
		At:       now,
	}) {
		p.verifyPair(r)
	}
}

// verifyPair latches §8 verification on both rows of a verified
// two-SDR pair and re-broadcasts full signal.update payloads so
// clients flip their verified badges without a refresh. The first
// verified pair wins the latch; later re-verifications are no-ops.
func (p *publisher) verifyPair(r location.PairResult) {
	for _, obs := range []location.Observation{r.A, r.B} {
		p.mu.Lock()
		if p.verified[obs.SignalID] {
			p.mu.Unlock()
			continue
		}
		p.verified[obs.SignalID] = true
		var sig *db.Signal
		if last, ok := p.lastSig[obs.SignalID]; ok {
			cp := *last
			cp.Verified = true
			p.lastSig[obs.SignalID] = &cp
			sig = &cp
		}
		p.mu.Unlock()

		log.Printf("verified %s (%.4f MHz) via %s + %s (confidence %.2f)",
			obs.SignalID, float64(obs.FreqHz)/1e6, r.A.SDRID, r.B.SDRID, r.Ver.Confidence)

		if p.db != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := p.db.MarkVerified(ctx, obs.SignalID); err != nil {
				log.Printf("mark verified %s: %v", obs.SignalID, err)
			}
			ver := &db.Verification{
				SignalID:   obs.SignalID,
				SDR1:       r.A.SDRID,
				SDR2:       r.B.SDRID,
				Verified:   true,
				Confidence: r.Ver.Confidence,
			}
			if err := p.db.InsertVerification(ctx, ver); err != nil {
				log.Printf("insert verification %s: %v", obs.SignalID, err)
			}
			cancel()
		}
		if sig != nil {
			p.queue("signal.update", sig)
		}
	}
}

// sweep retires signals that have been idle longer than the TTL,
// flagging their database rows inactive (rows are preserved for
// history, §11.2) and emitting signal.removed events (same client
// semantics as before: "no longer live").
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
					delete(p.lastSig, id) // §8 latch (p.verified) is kept
				}
			}
			p.mu.Unlock()

			for _, id := range removed {
				if p.db != nil {
					cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					if err := p.db.DeactivateSignal(cctx, id); err != nil {
						log.Printf("deactivate signal %s: %v", id, err)
					}
					cancel()
				}
				p.queue("signal.removed", map[string]string{"id": id})
			}

			// §14.4.3: SDRs that stopped sending frames are marked
			// inactive and their deactivation is broadcast.
			p.mu.Lock()
			var silent []sdrStatusEvent
			for id, st := range p.sdrs {
				if st.active && !st.lastFrame.IsZero() && now.Sub(st.lastFrame) > p.sdrSilenceTTL {
					st.active = false
					silent = append(silent, st.statusEvent(id))
				}
			}
			p.mu.Unlock()
			for _, ev := range silent {
				if p.db != nil {
					cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					if err := p.db.SetSDRActive(cctx, ev.ID, false); err != nil {
						log.Printf("deactivate sdr %s: %v", ev.ID, err)
					}
					cancel()
				}
				log.Printf("sdr %s silent > %v, marking inactive", ev.ID, p.sdrSilenceTTL)
				p.queue("sdr.status", ev)
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

// loadSDRs reads the configured SDR list from the sdr-capture config;
// used to seed sdr.status state (§14.4.3). A missing file is not fatal.
func loadSDRs(path string) []sdr.SDRCaptureConfig {
	cfg, err := sdr.LoadCaptureConfig(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("load SDR config %s: %v", path, err)
		}
		return nil
	}
	return cfg.SDRs
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
	onnx          *classify.ONNXClassifier
	rules         *classify.RuleClassifier
	minConfidence float64 // §16.1: below this, the rules result wins
}

// resolveClassification merges an ONNX result with the rules result:
// the frequency rules stay authoritative for the source enum (§6.5)
// and the bandwidth comes from the frame measurement. An ONNX result
// below min_confidence falls back to the rules classification
// entirely (its method stays "rules"), enforcing
// classifier.yaml onnx.min_confidence (§16.1).
func resolveClassification(res, rules *classify.Result, bandwidthHz, minConfidence float64) *classify.Result {
	if res.Confidence < minConfidence {
		return rules
	}
	res.Bandwidth = bandwidthHz
	res.Source = rules.Source
	return res
}

func (c *onnxFrameClassifier) Classify(freqHz uint64, bandwidthHz float64, spectrum *dsp.FFTResult) *classify.Result {
	// §6.5: signals.class must carry a source-enum value, never the method.
	// The frequency-rule classification is authoritative for the source; the
	// ONNX model refines modulation/subType but never overrides the source.
	rules := c.rules.Classify(freqHz, bandwidthHz, spectrum)
	if f := classify.ExtractFeatures(spectrum, freqHz); f != nil {
		if res, err := c.onnx.Classify(f.ToVector(), freqHz); err == nil && res != nil {
			return resolveClassification(res, rules, bandwidthHz, c.minConfidence)
		}
	}
	return rules
}

func main() {
	procCfgPath := flag.String("processor-config", config.GetEnv("PROCESSOR_CONFIG", "config/signal-processor.yaml"), "signal-processor YAML config file")
	clsCfgPath := flag.String("classifier-config", config.GetEnv("CLASSIFIER_CONFIG", "config/classifier.yaml"), "classifier YAML config file (min_confidence, model_path)")
	configPath := flag.String("config", config.GetEnv("SDR_CONFIG", "config/sdr-capture.yaml"), "sdr-capture config (SDR locations)")
	listenPortFlag := flag.Int("port", 0, "UDP listen port (overrides env/config)")
	thresholdFlag := flag.Float64("threshold", 0, "peak threshold dB (overrides env/config)")
	maxPeaksFlag := flag.Int("max-peaks", 0, "max peaks per frame (overrides env/config)")
	modelPathFlag := flag.String("model", "", "ONNX classifier model path (overrides env/config; empty = rules only)")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("signal-processor: ")

	// YAML files are the source of truth (§16.1); env and flags override.
	var procCfg config.SignalProcessorConfig
	if err := config.Load(*procCfgPath, &procCfg); err != nil {
		log.Printf("%v; using defaults/env", err)
	}
	var clsCfg config.ClassifierConfig
	if err := config.Load(*clsCfgPath, &clsCfg); err != nil {
		log.Printf("%v; using built-in classifier defaults", err)
	}

	listenPort := config.ResolveInt(*listenPortFlag, envInt("LISTEN_PORT", 0), procCfg.ListenPort, 9010)
	thresholdDB := config.ResolveFloat(*thresholdFlag, procCfg.PeakDetection.ThresholdDB, -60)
	maxPeaks := config.ResolveInt(*maxPeaksFlag, procCfg.PeakDetection.MaxPeaks, 20)
	modelPath := config.ResolveString(*modelPathFlag, os.Getenv("MODEL_PATH"), clsCfg.ModelPath)
	minConfidence := clsCfg.ONNX.MinConfidence
	if minConfidence <= 0 {
		minConfidence = 0.5 // classifier.yaml documents 0.5 as the default
	}

	// DSP components
	peakDetector := &dsp.PeakDetector{
		ThresholdDB: thresholdDB,
		MinSpacing:  config.ResolveInt(procCfg.PeakDetection.MinSpacingBins, 10),
		TopN:        maxPeaks,
	}
	ruleClassifier := classify.NewRuleClassifier()
	var classifier frameClassifier = ruleClassifier
	if modelPath != "" {
		onnxClassifier := classify.NewONNXClassifier(modelPath)
		if err := onnxClassifier.Load(); err != nil {
			log.Printf("ONNX classifier unavailable (%v); using rules", err)
		} else {
			defer onnxClassifier.Close()
			classifier = &onnxFrameClassifier{onnx: onnxClassifier, rules: ruleClassifier, minConfidence: minConfidence}
			log.Printf("loaded ONNX classifier %s (min_confidence=%.2f)", modelPath, minConfidence)
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
			// Startup reconciliation (§11.2): rows still flagged active
			// belong to a previous process — deactivate them so restarts
			// never leave zombie live signals. Anything still transmitting
			// is re-activated by its next upsert.
			rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
			n, err := database.DeactivateAllSignals(rctx)
			rcancel()
			if err != nil {
				log.Printf("reconcile active signals: %v", err)
			} else if n > 0 {
				log.Printf("reconcile: deactivated %d stale signal(s) from previous run", n)
			}
		}
	}

	// Publisher (DB + ws-hub + SDR locations)
	wsHubURL := os.Getenv("WS_HUB_URL")
	ttl := time.Duration(envInt("SIGNAL_TTL", 30)) * time.Second
	pub := newPublisher(database, wsHubURL, loadLocations(*configPath), ttl)
	pub.initSDRs(loadSDRs(*configPath))
	if wsHubURL != "" {
		log.Printf("emitting events to %s (ttl=%v)", wsHubURL, ttl)
	}

	// UDP listener
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", listenPort))
	if err != nil {
		log.Fatalf("resolve: %v", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer conn.Close()
	log.Printf("listening on UDP :%d  threshold=%.0f dB  max_peaks=%d",
		listenPort, thresholdDB, maxPeaks)

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

		// §14.4.3: per-frame SDR observation drives sdr.status events.
		pub.observeFrame(frame.SDRID, frame.FreqHz, time.Now())

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
	// ONNX features must stay byte-compatible with the one-sided positive
	// spectrum models/train.py was trained on, so the classifier sees the
	// positive half even though peak detection ran on the full wrapped
	// spectrum (D4, §5.3).
	posSpectrum := result.PositiveHalf()
	var events []signalEvent
	for _, p := range peaks {
		// Map FFT bin frequency to absolute frequency. The wrapped IQ FFT
		// reports a signed baseband offset (-fs/2..fs/2); add it to the
		// center and clamp at 0 (D4, §5.3).
		offsetHz := p.FreqHz
		peakHz := int64(frame.FreqHz) + int64(offsetHz)
		if peakHz < 0 {
			peakHz = 0
		}

		// Band identification
		band := dsp.IdentifyBand(uint64(peakHz))
		bandName := "Unknown"
		if band != nil {
			bandName = band.Name
		}

		// Classification
		result := classifier.Classify(uint64(peakHz), p.Bandwidth, posSpectrum)

		events = append(events, signalEvent{
			SDRID:     frame.SDRID,
			Timestamp: frame.Timestamp,
			CenterHz:  frame.FreqHz,
			PeakHz:    uint64(peakHz),
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
	method := "?"
	conf := 0.0
	bw := ev.Bandwidth
	if ev.Class != nil {
		mod = ev.Class.Modulation
		src = ev.Class.Source
		method = ev.Class.Method
		conf = ev.Class.Confidence
		bw = ev.Class.Bandwidth
	}
	// §6.5: class carries the source enum; method (rules|onnx) is recorded
	// separately, never folded into the class string.
	log.Printf("SIGNAL  %s  %.4f MHz  %s  %s/%s  %s  %.1f dB  BW:%.0f kHz  conf:%.2f",
		ev.SDRID,
		float64(ev.PeakHz)/1e6,
		ev.BandName,
		mod, src, method,
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
