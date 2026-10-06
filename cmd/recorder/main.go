// recorder — in-band signal recording (D1, §10.2): consumes the
// shared IQ stream (iq-ingest CONSUMERS fan-out), demodulates signals
// the processor currently tracks into per-signal WAV sessions, and
// enforces §11 retention (30 days / 50 GB).
//
// Run:
//
//	RECORDER_CONFIG=config/recorder.yaml DB_URL=... ./bin/recorder
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"sigint-workbench/internal/audio"
	"sigint-workbench/internal/config"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/record"
	"sigint-workbench/internal/sdr"
	"sigint-workbench/internal/tfr"
	"sigint-workbench/internal/ws"
)

// publishLevels posts a coarse audio.level event per open session at
// most 10 times per second (§10.6). Hub errors are logged at most
// once per 30 s and never stop the recorder.
func publishLevels(ctx context.Context, rec *record.Recorder, hubURL string) {
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond) // 10 Hz cap (§10.6)
	defer ticker.Stop()

	var lastErrLog time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for id, lvl := range rec.Levels() {
				payload, err := json.Marshal(map[string]any{"signalId": id, "level": lvl})
				if err != nil {
					continue
				}
				body, err := json.Marshal(ws.Event{Type: "audio.level", Payload: payload})
				if err != nil {
					continue
				}
				resp, err := client.Post(hubURL+"/api/events", "application/json", bytes.NewReader(body))
				if err != nil {
					if time.Since(lastErrLog) > 30*time.Second {
						lastErrLog = time.Now()
						log.Printf("audio.level: hub unreachable (%v)", err)
					}
					continue
				}
				resp.Body.Close()
			}
		}
	}
}

// resolveAudioSampleRate applies the §16.5 default and enforces the
// single supported rate: demod → AGC → Opus → WAV all run at 48 kHz
// (§10.2/§10.4), so any other configured value produced subtly wrong
// audio — it is now a hard startup error instead (A9).
func resolveAudioSampleRate(hz uint32) (uint32, error) {
	if hz == 0 {
		return 48000, nil // §16.5 documented default
	}
	if hz != 48000 {
		return 0, fmt.Errorf("audio.sample_rate %d: unsupported — the audio stack runs at 48000 Hz (§10.2/§10.4)", hz)
	}
	return hz, nil
}

func main() {
	cfgPath := flag.String("config", config.GetEnv("RECORDER_CONFIG", "config/recorder.yaml"), "recorder YAML config")
	listenPort := flag.Int("port", 0, "UDP IQ listen port (overrides env/config)")
	wsPortFlag := flag.Int("ws-port", 0, "live audio WS listen port (overrides env/config)")
	dir := flag.String("dir", "", "recordings directory (overrides env/config)")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("recorder: ")

	// YAML is the source of truth (§16.1); env and flags override.
	var cfg config.RecorderConfig
	if err := config.Load(*cfgPath, &cfg); err != nil {
		log.Printf("%v; using defaults", err)
		cfg.IQ.Enabled = true     // §16.5: raw IQ on by default
		cfg.IQ.MaxDurationS = 300 // §16.5: 5 min session cap
	}
	sampleRate, err := resolveAudioSampleRate(cfg.Audio.SampleRate)
	if err != nil {
		log.Fatalf("%v", err)
	}
	port := config.ResolveInt(*listenPort, config.GetEnvInt("RECORDER_UDP_PORT", 0), cfg.ListenPort, 9011)
	wsPort := config.ResolveInt(*wsPortFlag, config.GetEnvInt("RECORDER_WS_PORT", 0), cfg.Stream.ListenPort, 9012)
	dirPath := config.ResolveString(*dir, os.Getenv("RECORDINGS_DIR"), cfg.RecordingsDir, "./recordings")

	// Optional database (files-only mode without it).
	var database *db.DB
	if dbURL := os.Getenv("DB_URL"); dbURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var err error
		database, err = db.New(ctx, dbURL)
		cancel()
		if err != nil {
			log.Printf("database not available (%v), recording files only", err)
			database = nil
		} else {
			defer database.Close()
			log.Printf("connected to database")
		}
	}

	// Live audio (D1b, §10.4): per-signal Opus mux feeding the
	// /ws/audio server. Needs the opus build tag (libopus); without
	// it recording continues unaffected.
	var streamer *record.Streamer
	if audio.OpusAvailable {
		streamer = record.NewStreamer(record.StreamConfig{
			SampleRate: int(sampleRate),
			Channels:   cfg.Audio.Channels,
			BitrateBps: cfg.Stream.BitrateBps,
		})
	} else {
		log.Printf("opus support not built in (-tags opus); live audio disabled, recording unaffected")
	}

	rec := record.NewRecorder(record.Config{
		Dir:           dirPath,
		MaxAgeDays:    cfg.Retention.MaxAgeDays,
		MaxSizeGB:     cfg.Retention.MaxSizeGB,
		CloseSilence:  time.Duration(cfg.Capture.CloseSilenceS) * time.Second,
		MaxConcurrent: cfg.Capture.MaxConcurrent,
		IQEnabled:     cfg.IQ.Enabled,                                   // §10.5 raw IQ
		MaxDuration:   time.Duration(cfg.IQ.MaxDurationS) * time.Second, // §11.1 cap
		Streamer:      streamer,
	}, database)

	// §10.4: internal /ws/audio server on :9012 (unpublished; the
	// api-gateway relays browser connections to it).
	if streamer != nil {
		wsSrv := &http.Server{
			Addr:              fmt.Sprintf(":%d", wsPort),
			Handler:           record.WSAudioHandler(streamer),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Printf("live audio: ws://recorder:%d/ws/audio?signal=<id> (§10.4)", wsPort)
			if err := wsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("ws server: %v", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			wsSrv.Shutdown(shutdownCtx)
		}()
	}

	// §19 (D10): on-demand time-frequency renders over stored IQ
	// (§10.5/§11.3). Mounted internal-only (§3.2) on :9013; the
	// api-gateway proxies POST /api/recordings/{id}/tfr here (§13.1,
	// A3). The endpoint is always mounted — while tfr.enabled is false
	// it answers 404 (feature absent, not an error state, §19.3).
	tfrLimits := tfr.Limits{
		Enabled:  cfg.TFR.Enabled != nil && *cfg.TFR.Enabled,
		MaxSpanS: config.ResolveFloat(cfg.TFR.MaxSpanS, tfr.DefaultMaxSpan),
		MaxNFFT:  config.ResolveInt(cfg.TFR.MaxNFFT, tfr.DefaultMaxNFFT),
	}
	tfrPort := config.ResolveInt(config.GetEnvInt("RECORDER_TFR_PORT", 0), cfg.TFR.ListenPort, 9013)
	tfrLookup := func(ctx context.Context, id string) (*db.Recording, error) {
		if database == nil {
			return nil, db.ErrNotFound // files-only mode: nothing resolvable
		}
		return database.GetRecording(ctx, id)
	}
	tfrSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", tfrPort),
		Handler:           tfr.NewHandler(tfrLimits, tfrLookup).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("tfr: recorder :%d POST /api/recordings/{id}/tfr (enabled=%t, §19)", tfrPort, tfrLimits.Enabled)
		if err := tfrSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("tfr server: %v", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		tfrSrv.Shutdown(shutdownCtx)
	}()

	rx, frames, err := sdr.NewIQReceiver(port, 512)
	if err != nil {
		log.Fatalf("listen UDP :%d: %v", port, err)
	}
	log.Printf("listening on UDP :%d  dir=%s  retention=%dd/%.0fGB  rawIQ=%t cap=%ds",
		port, dirPath, cfg.Retention.MaxAgeDays, cfg.Retention.MaxSizeGB,
		cfg.IQ.Enabled, cfg.IQ.MaxDurationS)

	// Active-signal snapshot for ObserveFrame, refreshed on the poll
	// interval (§10.2: record what the processor currently tracks).
	var mu sync.Mutex
	var tracked []db.Signal
	refresh := func() {
		if database == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sigs, err := database.GetSignals(ctx, -90, -180, 90, 180)
		if err != nil {
			log.Printf("list signals: %v", err)
			return
		}
		mu.Lock()
		tracked = sigs
		mu.Unlock()
	}
	refresh()

	ctx, stop := context.WithCancel(context.Background())
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Printf("shutting down")
		stop()
		rx.Close()
	}()

	// §10.6 audio.level: when WS_HUB_URL is set, publish a coarse
	// (≤ 10 Hz) level event per actively demodulated signal to the
	// ws-hub ingest. Unset disables the feed — same convention as the
	// signal-processor's event publishing.
	if hubURL := os.Getenv("WS_HUB_URL"); hubURL != "" {
		go publishLevels(ctx, rec, hubURL)
	}

	// §4.5/§9.6: per-sender gap accounting for v2 streams, reported
	// periodically (recorder shares the ingest's IQ stream).
	seqs := sdr.NewSeqTracker()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				seqs.LogGaps()
			}
		}
	}()

	// Frame consumer: feed the shared IQ stream to the recorder.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-frames:
				if f.V2 {
					seqs.Observe(f.SDRID, f.Seq)
				}
				mu.Lock()
				snap := tracked
				mu.Unlock()
				rec.ObserveFrame(f, snap)
			}
		}
	}()

	// Housekeeping: refresh signals + close idle sessions on the poll
	// interval; apply retention hourly.
	poll := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	runRetention := func(now time.Time) {
		if cfg.Retention.MaxAgeDays <= 0 && cfg.Retention.MaxSizeGB <= 0 {
			return
		}
		rec.PurgeFiles(now, func(path string) {
			if database == nil {
				return
			}
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if rows, err := database.ListRecordings(cctx, "", 0); err == nil {
				for _, rr := range rows {
					if rr.FilePath == path {
						if err := database.DeleteRecording(cctx, rr.ID); err != nil && err != db.ErrNotFound {
							log.Printf("delete recording row %s: %v", rr.ID, err)
						}
					}
				}
			}
		})
		if database != nil && cfg.Retention.MaxAgeDays > 0 {
			cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if n, err := database.PurgeInactiveSignals(cctx,
				time.Duration(cfg.Retention.MaxAgeDays)*24*time.Hour); err != nil {
				log.Printf("purge inactive signals: %v", err)
			} else if n > 0 {
				log.Printf("archive purge: removed %d inactive signal(s) (§11)", n)
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			if n := rec.Close(); n > 0 {
				log.Printf("finalized %d recording(s) on shutdown", n)
			}
			return
		case <-poll.C:
			refresh()
			if n := rec.CloseIdle(time.Now()); n > 0 {
				log.Printf("finalized %d recording(s)", n)
			}
		case <-hourly.C:
			runRetention(time.Now())
		}
	}
}
