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
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"sigint-workbench/internal/config"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/record"
	"sigint-workbench/internal/sdr"
)

func main() {
	cfgPath := flag.String("config", config.GetEnv("RECORDER_CONFIG", "config/recorder.yaml"), "recorder YAML config")
	listenPort := flag.Int("port", 0, "UDP IQ listen port (overrides env/config)")
	dir := flag.String("dir", "", "recordings directory (overrides env/config)")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("recorder: ")

	// YAML is the source of truth (§16.1); env and flags override.
	var cfg config.RecorderConfig
	if err := config.Load(*cfgPath, &cfg); err != nil {
		log.Printf("%v; using defaults", err)
	}
	port := config.ResolveInt(*listenPort, config.GetEnvInt("RECORDER_PORT", 0), cfg.ListenPort, 9011)
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

	rec := record.NewRecorder(record.Config{
		Dir:           dirPath,
		MaxAgeDays:    cfg.Retention.MaxAgeDays,
		MaxSizeGB:     cfg.Retention.MaxSizeGB,
		CloseSilence:  time.Duration(cfg.Capture.CloseSilenceS) * time.Second,
		MaxConcurrent: cfg.Capture.MaxConcurrent,
	}, database)

	rx, frames, err := sdr.NewIQReceiver(port, 512)
	if err != nil {
		log.Fatalf("listen UDP :%d: %v", port, err)
	}
	log.Printf("listening on UDP :%d  dir=%s  retention=%dd/%.0fGB",
		port, dirPath, cfg.Retention.MaxAgeDays, cfg.Retention.MaxSizeGB)

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

	// Frame consumer: feed the shared IQ stream to the recorder.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-frames:
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
