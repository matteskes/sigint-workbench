// sdr-capture — Native SDR capture and IQ streaming service.
//
// Reads from SDR hardware (RTL-SDR, HackRF) and streams IQ data
// over UDP to the iq-ingest service.
//
// On macOS: run natively (not in Docker) for USB access.
// On Linux: can run in Docker with USB device passthrough.
//
// Build (macOS):  go build -tags rtlsdr -o bin/sdr-capture ./cmd/sdr-capture
// Run:            ./bin/sdr-capture -config config/sdr-capture.yaml
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sigint-workbench/internal/sdr"
)

func main() {
	configPath := flag.String("config", "config/sdr-capture.yaml", "path to YAML config")
	flag.Parse()

	cfg, err := sdr.LoadCaptureConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdr-capture: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("sdr-capture: starting with %d SDR(s)\n", len(cfg.SDRs))

	// Create streamers for each SDR
	streamers := make([]*sdr.IQStreamer, 0, len(cfg.SDRs))
	for _, sc := range cfg.SDRs {
		streamer, err := sdr.NewIQStreamer(sc.StreamHost, sc.StreamPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sdr-capture: streamer for %s: %v\n", sc.ID, err)
			os.Exit(1)
		}
		streamers = append(streamers, streamer)
		fmt.Printf("sdr-capture: %s → %s:%d\n", sc.ID, sc.StreamHost, sc.StreamPort)
	}

	// TODO: Open SDR devices, read IQ, stream in goroutines
	// For each SDR:
	//   1. Create device via sdr.NewRTLSDR(sc.ID, sc.USBIndex)
	//   2. device.Open()
	//   3. device.SetFrequency(sc.DefaultFreq)
	//   4. device.SetSampleRate(sc.DefaultBW)
	//   5. device.SetGain(sc.DefaultGain)
	//   6. Goroutine: loop { device.ReadIQ(buf); streamer.Send(frame) }

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	fmt.Printf("\nsdr-capture: shutting down (%v)\n", sig)

	for _, s := range streamers {
		s.Close()
	}
}