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
			continue
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
			log.Printf("[%s] send error: %v", slot.cfg.ID, err)
		}
	}
}

// scanLoop implements the D3 frequency sweep (§7.1): step across the
// resolved range at scanStep resolution, dwelling scanDwell per
// frequency. A manual tune (§7.4) parks the loop until restart.
func scanLoop(slot *sdrSlot, exit <-chan struct{}) {
	ticker := time.NewTicker(slot.scanDwell)
	defer ticker.Stop()
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
		slot.mu.Unlock()
		if paused {
			continue
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

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Printf("control API listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("control server: %v", err)
		}
	}()
	return srv
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
		fmt.Println("  simulator-0  [Simulator]  24-1700 MHz  BW:10 MHz")
		fmt.Println("  (requires -tags rtlsdr to query USB hardware)")
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
