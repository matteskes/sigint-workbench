// sdr-capture - Native SDR capture and IQ streaming service.
//
// Reads from SDR hardware (RTL-SDR, HackRF) or a built-in simulator,
// and streams IQ frames over UDP to the iq-ingest service.
//
// Build (macOS, real hardware):
//
//	go build -tags rtlsdr -o bin/sdr-capture ./cmd/sdr-capture
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
	"syscall"
	"time"

	"sigint-workbench/internal/sdr"
)

// sdrSlot tracks one SDR device and its streaming state.
type sdrSlot struct {
	cfg      sdr.SDRCaptureConfig
	device   sdr.SDR
	streamer *sdr.IQStreamer
	freqHz   uint64
	gainDB   float64
	mu       sync.Mutex
}

func (s *sdrSlot) setFrequency(mhz float64) error {
	hz := uint64(mhz * 1e6)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.device.SetFrequency(hz); err != nil {
		return err
	}
	s.freqHz = hz
	log.Printf("[%s] frequency -> %.4f MHz", s.cfg.ID, mhz)
	return nil
}

func (s *sdrSlot) setGain(db float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.device.SetGain(db); err != nil {
		return err
	}
	s.gainDB = db
	log.Printf("[%s] gain -> %.1f dB", s.cfg.ID, db)
	return nil
}

func (s *sdrSlot) status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := s.device.Metadata()
	return map[string]any{
		"id":       s.cfg.ID,
		"driver":   s.cfg.Driver,
		"model":    meta.Model,
		"freq_mhz": s.freqHz / 1e6,
		"gain_db":  s.gainDB,
		"bw_hz":    s.cfg.DefaultBW,
		"mode":     s.cfg.Mode,
		"stream":   fmt.Sprintf("%s:%d", s.cfg.StreamHost, s.cfg.StreamPort),
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
		slot.mu.Lock()
		freqHz := slot.freqHz
		slot.mu.Unlock()
		frame := &sdr.IQFrame{
			SDRID:      slot.cfg.ID,
			FreqHz:     freqHz,
			SampleRate: slot.cfg.DefaultBW,
			Timestamp:  time.Now(),
			Samples:    buf[:n],
		}
		if err := slot.streamer.Send(frame); err != nil {
			log.Printf("[%s] send error: %v", slot.cfg.ID, err)
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

		streamer, err := sdr.NewIQStreamer(sc.StreamHost, sc.StreamPort)
		if err != nil {
			log.Fatalf("streamer %s: %v", sc.ID, err)
		}
		slots = append(slots, &sdrSlot{
			cfg:      sc,
			device:   device,
			streamer: streamer,
			freqHz:   sc.DefaultFreq,
			gainDB:   sc.DefaultGain,
		})
		log.Printf("%s: %s  %.4f MHz  %.1f dB  %.2f MHz  -> %s:%d",
			sc.ID, device.Metadata().Model,
			float64(sc.DefaultFreq)/1e6, sc.DefaultGain,
			float64(sc.DefaultBW)/1e6, sc.StreamHost, sc.StreamPort)
	}

	// Start IQ read loops
	exit := make(chan struct{})
	bufSize := sdr.MaxIQSamplesPerFrame * 2
	for _, slot := range slots {
		buf := make([]int16, bufSize)
		go iqReadLoop(slot, buf, exit)
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
