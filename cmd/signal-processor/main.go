// signal-processor - FFT, peak detection, band identification, classification.
//
// Receives IQ frames over UDP from iq-ingest, runs the DSP pipeline:
//
//	IQ int16 -> float64 -> IQ FFT -> peak detect -> band ID -> classify
//
// Detected signals are logged to stdout in a structured format.
//
// Run:
//
//	./bin/signal-processor -port 9010
//
// Or with env vars (Docker):
//
//	LISTEN_PORT=9010 ./bin/signal-processor
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	"sigint-workbench/internal/classify"
	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
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

func main() {
	listenPort := flag.Int("port", envInt("LISTEN_PORT", 9010), "UDP listen port")
	thresholdDB := flag.Float64("threshold", -60, "peak threshold dB")
	maxPeaks := flag.Int("max-peaks", 20, "max peaks per frame")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("signal-processor: ")

	// DSP components
	peakDetector := &dsp.PeakDetector{
		ThresholdDB: *thresholdDB,
		MinSpacing:  10,
		TopN:        *maxPeaks,
	}
	classifier := classify.NewRuleClassifier()

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
	go func() {
		sig := <-sigCh
		log.Printf("shutting down (%v)", sig)
		conn.Close()
		time.Sleep(50 * time.Millisecond)
		os.Exit(0)
	}()

	// Throttle: log at most once per 2 seconds per SDR+freq
	lastLog := make(map[string]time.Time)

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

		// Log events (throttled)
		now := time.Now()
		for _, ev := range events {
			key := fmt.Sprintf("%s:%d", ev.SDRID, ev.PeakHz/1000) // per SDR+freq (kHz)
			if last, ok := lastLog[key]; ok && now.Sub(last) < 2*time.Second {
				continue
			}
			lastLog[key] = now
			logSignal(ev)
		}
	}
}

// processFrame runs the DSP pipeline on one IQ frame.
func processFrame(frame *sdr.IQFrame, pd *dsp.PeakDetector, rc *classify.RuleClassifier) []signalEvent {
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
		result := rc.Classify(peakHz, p.Bandwidth, result)

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