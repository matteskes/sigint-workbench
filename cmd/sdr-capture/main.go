// sdr-capture - Native SDR capture and IQ streaming service.
//
// Reads from SDR hardware (RTL-SDR, HackRF) or a built-in simulator,
// and streams IQ frames over UDP to the iq-ingest service.
//
// Build (macOS, real hardware):
//
//	go build -tags rtlsdr -o bin/sdr-capture ./cmd/sdr-capture
//	go build -tags "rtlsdr,hackrf" -o bin/sdr-capture ./cmd/sdr-capture
//
// Build (no hardware, simulator only):
//
//	go build -o bin/sdr-capture ./cmd/sdr-capture
//
// Run:
//
//	./bin/sdr-capture -config config/sdr-capture.yaml
//	./bin/sdr-capture -sim -freq 146.52 -gain 40
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"sigint-workbench/internal/sdr"
)

// sdrSlot tracks one SDR device and its streaming state. A slot is
// driven either by manual control only (mode "monitor") or by the D3
// scan loop (§7.1) when configured with mode "scanner" or "both".
type sdrSlot struct {
	cfg      sdr.SDRCaptureConfig
	device   sdr.SDR
	streamer *sdr.IQStreamer
	freqHz   uint64
	gainDB   float64
	mu       sync.Mutex

	scanning   bool // D3 scan loop attached to this slot
	scanPaused bool // a manual tune parked the sweep (§7.4)

	// Scan loop parameters (§7.1 defaults: 100 kHz step, 50 ms dwell).
	scanStep  uint64
	scanDwell time.Duration
	scanMinHz uint64
	scanMaxHz uint64

	// Last successful IQ read (unix nanos); 0 until data flows. Backs
	// the §7.4 status "active" field.
	lastRead atomic.Int64

	// §4.5 v2 stream state (used when stream_format is sdr2): v2
	// selects the wire format; seq is the per-sender sequence number
	// (+1 per frame, starts at 0); sampleIndex is the stream-wide
	// count of I/Q pairs already streamed. Guarded by mu alongside
	// freqHz.
	v2          bool
	seq         uint64
	sampleIndex uint64
}

func (s *sdrSlot) setFrequency(mhz float64) error {
	hz := uint64(mhz * 1e6)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.device.SetFrequency(hz); err != nil {
		return err
	}
	s.freqHz = hz
	// §7.4: a manual tune parks the D3 scan loop until restart.
	if s.scanning {
		s.scanPaused = true
		log.Printf("[%s] frequency -> %.4f MHz (scan loop paused)", s.cfg.ID, mhz)
	} else {
		log.Printf("[%s] frequency -> %.4f MHz", s.cfg.ID, mhz)
	}
	return nil
}

// setScanFrequency tunes the device as part of the D3 sweep (§7.1).
// Unlike a manual setFrequency it does not pause the scan loop.
func (s *sdrSlot) setScanFrequency(hz uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.device.SetFrequency(hz); err != nil {
		return err
	}
	s.freqHz = hz
	return nil
}

// setScanEnabled parks or resumes the D3 scan loop at runtime (§7.4).
// Both directions are idempotent. Parking keeps IQ streaming at the
// current frequency; resuming clears a boot park (scan_autostart:
// false) or a manual-tune park. The sweep cursor itself re-syncs from
// the slot's current frequency inside scanLoop, so a resume continues
// the sweep from wherever the device is tuned right now.
func (s *sdrSlot) setScanEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanPaused = !enabled
	if enabled {
		log.Printf("[%s] scan loop resumed from %.4f MHz", s.cfg.ID, float64(s.freqHz)/1e6)
	} else {
		log.Printf("[%s] scan loop parked at %.4f MHz", s.cfg.ID, float64(s.freqHz)/1e6)
	}
}

// nextScanFreq returns the next sweep frequency after cur (§7.1):
// f = min + k·step, wrapping to min once the step would exceed max.
// ok is false when the range is degenerate (step 0 or min >= max),
// which disables the sweep.
func nextScanFreq(cur, step, min, max uint64) (next uint64, ok bool) {
	if step == 0 || min >= max {
		return 0, false
	}
	if cur < min || cur >= max {
		return min, true
	}
	next = cur + step
	if next > max {
		next = min
	}
	return next, true
}

// resolveScanRange resolves the effective sweep range for one device
// (§7.1): configured scan.min_hz/scan.max_hz with zero meaning "driver
// default", with the result clamped to the driver's capability range.
// ok is false when no usable range remains.
func resolveScanRange(cfg sdr.ScanConfig, meta sdr.SDRMetadata) (min, max uint64, ok bool) {
	min, max = cfg.MinHz, cfg.MaxHz
	if min == 0 {
		min = meta.FreqMin
	}
	if max == 0 {
		max = meta.FreqMax
	}
	if min < meta.FreqMin {
		min = meta.FreqMin
	}
	if max > meta.FreqMax {
		max = meta.FreqMax
	}
	return min, max, min < max
}

// appliedGainDB reports the gain the hardware actually applied for a
// requested figure. Drivers that snap requests onto a supported step
// (RTL-SDR, §15.3 defect 4) are queried via the optional
// AppliedGainDB interface, so the §7.4 status, the startup log line
// and §5.6 applied_gain_db all track the hardware rather than the
// config. Drivers without the interface report the request as before.
func appliedGainDB(dev sdr.SDR, requested float64) float64 {
	if a, ok := dev.(interface{ AppliedGainDB() (float64, bool) }); ok {
		if g, ok := a.AppliedGainDB(); ok {
			return g
		}
	}
	return requested
}

func (s *sdrSlot) setGain(db float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.device.SetGain(db); err != nil {
		return err
	}
	s.gainDB = appliedGainDB(s.device, db)
	log.Printf("[%s] gain -> %.1f dB", s.cfg.ID, s.gainDB)
	return nil
}

func (s *sdrSlot) status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := s.device.Metadata()
	// §7.4: a device is active while IQ frames are still flowing.
	active := false
	if last := s.lastRead.Load(); last != 0 && time.Since(time.Unix(0, last)) < 5*time.Second {
		active = true
	}
	return map[string]any{
		"id":          s.cfg.ID,
		"driver":      s.cfg.Driver,
		"model":       meta.Model,
		"active":      active,
		"freq_hz":     s.freqHz,
		"freq_mhz":    float64(s.freqHz) / 1e6,
		"gain_db":     s.gainDB,
		"bw_hz":       s.cfg.DefaultBW,
		"sample_rate": s.cfg.DefaultBW,
		"mode":        s.cfg.Mode,
		"scanning":    s.scanning,
		"scan_paused": s.scanPaused,
		"stream":      fmt.Sprintf("%s:%d", s.cfg.StreamHost, s.cfg.StreamPort),
	}
}

// iqReadLoop continuously reads IQ from the SDR and streams over UDP.
func iqReadLoop(slot *sdrSlot, buf []int16, exit <-chan struct{}) {
	var lastSendErrLog time.Time
	var lastZeroLog time.Time
	var firstFrame bool
	for {
		select {
		case <-exit:
			return
		default:
		}
		n, err := slot.device.ReadIQ(buf)
		if err != nil {
			select {
			case <-exit:
				return
			default:
			}
			log.Printf("[%s] read error: %v", slot.cfg.ID, err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if n%2 != 0 {
			n--
		}
		if n == 0 {
			// Rate-limit: a device whose USB bulk pipe has wedged
			// can return zero bytes forever, and an unlogged loop
			// here left the whole downstream (SDR list, signals,
			// spectrum) silently empty with a healthy-looking log.
			if now := time.Now(); now.Sub(lastZeroLog) >= 5*time.Second {
				lastZeroLog = now
				log.Printf("[%s] read returned no samples (suppressing repeats for 5s)", slot.cfg.ID)
			}
			continue
		}
		if !firstFrame {
			firstFrame = true
			log.Printf("[%s] streaming: first frame (%d samples)", slot.cfg.ID, n)
		}
		slot.lastRead.Store(time.Now().UnixNano())
		slot.mu.Lock()
		freqHz := slot.freqHz
		v2 := slot.v2
		seq := slot.seq
		slot.seq++
		sampleIndex := slot.sampleIndex
		slot.sampleIndex += uint64(n) / 2
		slot.mu.Unlock()
		// §4.5: the v2 anchor is CLOCK_REALTIME read in the driver
		// read loop, stamped at the frame's first sample.
		frame := &sdr.IQFrame{
			SDRID:       slot.cfg.ID,
			FreqHz:      freqHz,
			SampleRate:  slot.cfg.DefaultBW,
			Timestamp:   time.Now(),
			Samples:     buf[:n],
			V2:          v2,
			Seq:         seq,
			SampleIndex: sampleIndex,
		}
		if err := slot.streamer.Send(frame); err != nil {
			// Rate-limit the log: while iq-ingest is not up yet
			// (e.g. compose is still building images on the first
			// run) this loop runs at frame rate and would flood the
			// terminal one line per datagram. Sending itself never
			// pauses — frames are dropped, the loop keeps reading.
			if now := time.Now(); now.Sub(lastSendErrLog) >= 5*time.Second {
				lastSendErrLog = now
				log.Printf("[%s] send error: %v (suppressing repeats for 5s)",
					slot.cfg.ID, err)
			}
		}
	}
}

// watchStreamStall reports a slot that should be streaming but is not:
// neither the zero-read log nor the send-error log can fire when a
// blocking cgo read (rtlsdr_read_sync has no timeout) parks the
// iqReadLoop goroutine on a wedged USB bulk pipe — the macOS failure
// that leaves the whole downstream silently empty. Recovery: unplug /
// replug the dongle, then restart make dev (which restarts capture).
func watchStreamStall(slot *sdrSlot, exit <-chan struct{}) {
	const (
		stallAfter = 15 * time.Second
		warnEvery  = 30 * time.Second
	)
	start := time.Now()
	var lastWarn time.Time
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-exit:
			return
		case <-ticker.C:
		}
		last := slot.lastRead.Load()
		if last == 0 {
			if time.Since(start) < stallAfter {
				continue // startup grace: the first read may take a moment
			}
		} else if age := time.Since(time.Unix(0, last)); age <= stallAfter {
			continue // healthy: data flowed recently
		}
		if now := time.Now(); !lastWarn.IsZero() && now.Sub(lastWarn) < warnEvery {
			continue
		}
		lastWarn = time.Now()
		if last == 0 {
			log.Printf("[%s] WARNING: no samples ever received — USB bulk reads are stalled; unplug/replug the dongle and restart make dev", slot.cfg.ID)
		} else {
			log.Printf("[%s] WARNING: no samples for %s — stream stalled; unplug/replug the dongle if it does not recover",
				slot.cfg.ID, time.Since(time.Unix(0, last)).Round(time.Second))
		}
	}
}

// scanLoop implements the D3 frequency sweep (§7.1): step across the
// resolved range at scanStep resolution, dwelling scanDwell per
// frequency. A manual tune (§7.4) parks the loop until restart or a
// runtime POST /api/v1/scan resume; boot parks come from
// scan_autostart: false.
func scanLoop(slot *sdrSlot, exit <-chan struct{}) {
	ticker := time.NewTicker(slot.scanDwell)
	defer ticker.Stop()
	wasPaused := true // force a cursor sync on the first unparked tick
	slot.mu.Lock()
	cur := slot.freqHz
	slot.mu.Unlock()
	for {
		select {
		case <-exit:
			return
		case <-ticker.C:
		}
		slot.mu.Lock()
		paused := slot.scanPaused
		if paused {
			// Parked (§7.4): keep the cursor on the slot's live
			// frequency (manual tune or boot default) so the
			// parked state itself never goes stale.
			cur = slot.freqHz
		}
		slot.mu.Unlock()
		if paused {
			wasPaused = true
			continue
		}
		if wasPaused {
			// Resume edge (park lifted): re-sync the cursor from
			// the slot's current frequency — a manual tune can
			// land between ticks, so the last parked read is not
			// trustworthy. From here on the cursor advances on
			// its own (§7.1), even past failing tunes.
			slot.mu.Lock()
			cur = slot.freqHz
			slot.mu.Unlock()
			wasPaused = false
		}
		next, ok := nextScanFreq(cur, slot.scanStep, slot.scanMinHz, slot.scanMaxHz)
		if !ok {
			continue
		}
		// Advance the sweep cursor even when the tune fails so one
		// bad frequency cannot wedge the scan (§7.1); the slot keeps
		// reporting its last successfully tuned frequency.
		cur = next
		if err := slot.setScanFrequency(next); err != nil {
			log.Printf("[%s] scan: %.4f MHz: %v", slot.cfg.ID, float64(next)/1e6, err)
		}
	}
}

// startControlServer runs a lightweight HTTP API for runtime SDR control.
func startControlServer(slots []*sdrSlot, addr string) *http.Server {
	srv := &http.Server{Addr: addr, Handler: controlMux(slots)}
	go func() {
		log.Printf("control API listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("control server: %v", err)
		}
	}()
	return srv
}

// controlMux builds the control-API routes. Split from
// startControlServer so tests can exercise the handlers with
// httptest against fake devices.
func controlMux(slots []*sdrSlot) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		out := make([]map[string]any, 0, len(slots))
		for _, s := range slots {
			out = append(out, s.status())
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/v1/frequency", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID      string  `json:"id"`
			FreqMHz float64 `json:"freq_mhz"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, s := range slots {
			if s.cfg.ID == req.ID {
				if err := s.setFrequency(req.FreqMHz); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": req.ID, "freq_mhz": req.FreqMHz})
				return
			}
		}
		http.Error(w, "unknown SDR id", http.StatusNotFound)
	})

	mux.HandleFunc("/api/v1/gain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     string  `json:"id"`
			GainDB float64 `json:"gain_db"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, s := range slots {
			if s.cfg.ID == req.ID {
				if err := s.setGain(req.GainDB); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": req.ID, "gain_db": req.GainDB})
				return
			}
		}
		http.Error(w, "unknown SDR id", http.StatusNotFound)
	})

	mux.HandleFunc("/api/v1/scan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, s := range slots {
			if s.cfg.ID == req.ID {
				if !s.scanning {
					// §7.4: no scan loop is attached (mode
					// "monitor", or a degenerate sweep range)
					// — there is nothing to park or resume.
					http.Error(w,
						"device has no scan loop (mode "+s.cfg.Mode+")",
						http.StatusConflict)
					return
				}
				s.setScanEnabled(req.Enabled)
				st := s.status()
				st["ok"] = true
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(st)
				return
			}
		}
		http.Error(w, "unknown SDR id", http.StatusNotFound)
	})

	return mux
}

// createSDR builds the appropriate SDR driver for the given config.
func createSDR(cfg sdr.SDRCaptureConfig, useSim bool) (sdr.SDR, error) {
	if useSim || cfg.Driver == "simulator" {
		return sdr.NewSimulator(cfg.DefaultFreq, cfg.DefaultBW), nil
	}
	switch cfg.Driver {
	case "rtlsdr":
		return sdr.NewRTLSDR(cfg.ID, cfg.USBIndex)
	case "hackrf":
		return sdr.NewHackRF(cfg.ID, cfg.Serial)
	default:
		return nil, fmt.Errorf("unknown driver %q", cfg.Driver)
	}
}

func main() {
	configPath := flag.String("config", "config/sdr-capture.yaml", "path to YAML config")
	simMode := flag.Bool("sim", false, "use built-in simulator (no hardware)")
	freqMHz := flag.Float64("freq", 0, "center frequency MHz (sim quick-start)")
	gainDB := flag.Float64("gain", 0, "RF gain dB (overrides config)")
	listen := flag.String("listen", "127.0.0.1:9090", "control API listen address")
	showDevices := flag.Bool("devices", false, "list SDR devices and exit")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("sdr-capture: ")

	if *showDevices {
		fmt.Println("Available SDR devices:")
		n := sdr.DeviceCount()
		for i := 0; i < n; i++ {
			if product, serial, ok := sdr.DeviceUSBStrings(i); ok {
				fmt.Printf("  [%d] %s  serial=%s\n", i, product, serial)
			}
		}
		if n == 0 {
			fmt.Println("  (no RTL-SDR hardware found — untagged build, or no dongles attached)")
		}
		fmt.Println("  simulator-0  [Simulator]  24-1700 MHz  BW:10 MHz (use -sim)")
		return
	}

	var cfg *sdr.CaptureConfig
	var err error
	if *simMode && *freqMHz > 0 {
		g := 40.0
		if *gainDB > 0 {
			g = *gainDB
		}
		cfg = &sdr.CaptureConfig{
			SDRs: []sdr.SDRCaptureConfig{{
				ID:          "simulator-0",
				Driver:      "simulator",
				DefaultFreq: uint64(*freqMHz * 1e6),
				DefaultGain: g,
				DefaultBW:   2_400_000,
				Mode:        "monitor",
				StreamHost:  "127.0.0.1",
				StreamPort:  9000,
			}},
		}
		log.Printf("simulator: %.4f MHz, %.1f dB, 2.4 MHz BW", *freqMHz, g)
	} else {
		cfg, err = sdr.LoadCaptureConfig(*configPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
	}
	if *simMode {
		log.Println("SIMULATOR mode (no hardware)")
	}

	// Create SDR slots
	log.Printf("IQ wire format: %s (§4.5)", cfg.WireFormat())
	slots := make([]*sdrSlot, 0, len(cfg.SDRs))
	for _, sc := range cfg.SDRs {
		device, err := createSDR(sc, *simMode)
		if err != nil {
			log.Fatalf("create %s: %v", sc.ID, err)
		}
		if err := device.Open(); err != nil {
			log.Fatalf("open %s: %v", sc.ID, err)
		}
		if err := device.SetFrequency(sc.DefaultFreq); err != nil {
			log.Fatalf("%s: set frequency: %v", sc.ID, err)
		}
		if err := device.SetSampleRate(sc.DefaultBW); err != nil {
			log.Fatalf("%s: set sample rate: %v", sc.ID, err)
		}
		if err := device.SetGain(sc.DefaultGain); err != nil {
			log.Fatalf("%s: set gain: %v", sc.ID, err)
		}

		gainDB := appliedGainDB(device, sc.DefaultGain)

		streamer, err := sdr.NewIQStreamer(sc.StreamHost, sc.StreamPort)
		if err != nil {
			log.Fatalf("streamer %s: %v", sc.ID, err)
		}
		meta := device.Metadata()
		slot := &sdrSlot{
			cfg:      sc,
			device:   device,
			streamer: streamer,
			freqHz:   sc.DefaultFreq,
			gainDB:   gainDB,
			v2:       cfg.WireFormat() == "sdr2",
		}
		// §7.1: scanner-mode devices are driven by the D3 sweep loop.
		if sc.Mode == "scanner" || sc.Mode == "both" {
			if minHz, maxHz, ok := resolveScanRange(cfg.Scan, meta); ok {
				slot.scanning = true
				slot.scanStep = cfg.Scan.Step()
				slot.scanDwell = cfg.Scan.Dwell()
				slot.scanMinHz = minHz
				slot.scanMaxHz = maxHz
				// §7.4: scan_autostart: false boots the sweep
				// loop attached but parked at default_freq — the
				// device monitors until POST /api/v1/scan
				// enables the sweep for this session.
				if !sc.ScanAutoStartEnabled() {
					slot.scanPaused = true
					log.Printf("[%s] scan autostart disabled: parked at %.4f MHz (enable via POST /api/v1/scan {\"enabled\":true})",
						sc.ID, float64(sc.DefaultFreq)/1e6)
				}
			} else {
				log.Printf("[%s] scan disabled: no usable frequency range (driver %.0f-%.0f MHz)",
					sc.ID, float64(meta.FreqMin)/1e6, float64(meta.FreqMax)/1e6)
			}
		}
		slots = append(slots, slot)
		log.Printf("%s: %s  %.4f MHz  %.1f dB  %.2f MHz  -> %s:%d",
			sc.ID, meta.Model,
			float64(sc.DefaultFreq)/1e6, gainDB,
			float64(sc.DefaultBW)/1e6, sc.StreamHost, sc.StreamPort)
	}

	// Start IQ read loops and D3 scan loops (§7.1).
	exit := make(chan struct{})
	bufSize := sdr.MaxIQSamplesPerFrame * 2
	for _, slot := range slots {
		buf := make([]int16, bufSize)
		go iqReadLoop(slot, buf, exit)
		go watchStreamStall(slot, exit)
		if slot.scanning {
			log.Printf("[%s] scan: %.4f-%.4f MHz  step %.0f kHz  dwell %d ms",
				slot.cfg.ID,
				float64(slot.scanMinHz)/1e6, float64(slot.scanMaxHz)/1e6,
				float64(slot.scanStep)/1e3, slot.scanDwell.Milliseconds())
			go scanLoop(slot, exit)
		}
	}

	// Start HTTP control server
	ctrlSrv := startControlServer(slots, *listen)

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("shutting down (%v)", sig)

	close(exit)
	ctrlSrv.Close()
	for _, slot := range slots {
		slot.streamer.Close()
		slot.device.Close()
		log.Printf("[%s] closed", slot.cfg.ID)
	}
}
