// Command rtl-calibrate measures a per-device §5.6 power-calibration
// offset (calibration_offset_db) for one RTL-SDR against a reference
// emitter of estimated level, and prints it ready to paste into
// config/sdr-capture.yaml. It is the tooling half of the on-air
// calibration procedure in docs/HARDWARE.md §6 (Phase 3, slice 3).
//
// The measurement matches the live pipeline exactly — int16/32768
// normalization, the wrapped ComputeIQFFT spectrum, and the
// strongest-bin power_db that signal-processor would report for the
// same 1024-pair frame (§5.2/§5.4) — so the offset computed here is
// the offset the pipeline needs, with no scale conversion.
//
//	power_dbm = power_db − applied_gain_db + calibration_offset_db
//		⇒ offset = expected_dbm − mean_power_db + gain_db   (§5.6)
//
// The tuner applies the nearest supported gain step; that sub-dB delta
// is absorbed into the offset, so the value is only valid at the
// calibrated gain — keep default_gain identical in the capture config.
//
// Usage:
//
//	rtl-calibrate -freq 98700000 -expected-dbm -40 [-index 0]
//	    [-gain 40] [-duration 30s] [-rate 2400000] [-tune-offset 250000]
//
// Build (real hardware — cgo, librtlsdr):
//
//	go build -tags rtlsdr -o bin/rtl-calibrate ./cmd/rtl-calibrate
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
)

// dcGuardHz is the half-width of the guard band around baseband 0 Hz.
// The RTL2832U places a large DC-carrier spike at baseband 0; bins
// within ±20 kHz of it are excluded from the peak search regardless of
// tuning (docs/HARDWARE.md §6.2).
const dcGuardHz = 20_000.0

func main() {
	index := flag.Int("index", 0, "RTL-SDR USB index (see cmd/rtl-list)")
	freq := flag.Float64("freq", 0,
		"reference carrier frequency in Hz (required)")
	gain := flag.Float64("gain", 40,
		"tuner gain in dB — calibration is valid at this gain only")
	expected := flag.Float64("expected-dbm", 0,
		"estimated carrier level in dBm at the antenna (required)")
	duration := flag.Duration("duration", 30*time.Second,
		"measurement window")
	rate := flag.Uint("rate", 2_400_000, "sample rate in Hz")
	pairs := flag.Int("pairs", 1024,
		"IQ pairs per frame (pipeline wire limit: 1024)")
	tuneOffset := flag.Float64("tune-offset", 250_000,
		"tune this many Hz BELOW the carrier so it lands away from the "+
			"DC spike (0 = tune dead-on)")
	stride := flag.Int("stride", 10,
		"FFT every Nth frame read (CPU relief; does not affect the stats)")
	flag.Parse()

	seen := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if !seen["freq"] || !seen["expected-dbm"] {
		fmt.Fprintln(os.Stderr,
			"rtl-calibrate: -freq and -expected-dbm are required")
		flag.Usage()
		os.Exit(2)
	}
	if *pairs < 64 || *pairs > sdr.MaxIQSamplesPerFrame {
		fmt.Fprintf(os.Stderr,
			"rtl-calibrate: -pairs must be 64..%d (pipeline frame sizes)\n",
			sdr.MaxIQSamplesPerFrame)
		os.Exit(2)
	}
	if *rate == 0 || *stride < 1 {
		fmt.Fprintln(os.Stderr,
			"rtl-calibrate: -rate must be > 0 and -stride >= 1")
		os.Exit(2)
	}
	center := int64(*freq) - int64(*tuneOffset)
	if center <= 0 {
		fmt.Fprintf(os.Stderr,
			"rtl-calibrate: -tune-offset %g Hz exceeds carrier %g Hz\n",
			*tuneOffset, *freq)
		os.Exit(2)
	}

	dev, err := sdr.NewRTLSDR("cal", *index)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rtl-calibrate: %v\n", err)
		os.Exit(1)
	}
	if err := dev.Open(); err != nil {
		fatal("open", err)
	}
	defer dev.Close()
	if err := dev.SetFrequency(uint64(center)); err != nil {
		fatal("set frequency", err)
	}
	if err := dev.SetSampleRate(uint32(*rate)); err != nil {
		fatal("set sample rate", err)
	}
	if err := dev.SetGain(*gain); err != nil {
		fatal("set gain", err)
	}
	// The tuner applies its nearest supported step (§15.3 defect 4);
	// the offset math must use the applied figure so it matches what
	// the live pipeline reports as applied_gain_db.
	applied := *gain
	if g, ok := dev.AppliedGainDB(); ok {
		applied = g
	}

	meta := dev.Metadata()
	fmt.Printf("device:  USB index %d, %s, serial %q\n",
		*index, meta.Model, meta.Serial)
	fmt.Printf("carrier: %.4f MHz, expected %.1f dBm at the antenna\n",
		*freq/1e6, *expected)
	fmt.Printf("tuning:  center %.4f MHz (carrier at +%.0f kHz baseband, "+
		"DC guard ±%.0f kHz)\n",
		float64(center)/1e6, *tuneOffset/1e3, dcGuardHz/1e3)
	fmt.Printf("chain:   gain %.1f dB applied (%.1f dB requested), "+
		"rate %.2f MHz, %d pairs/frame, FFT every %dth frame\n",
		applied, *gain, float64(*rate)/1e6, *pairs, *stride)
	fmt.Printf("window:  %s (Ctrl-C reports the partial window)\n",
		*duration)
	fmt.Println()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	buf := make([]int16, *pairs*2)
	iq := make([]float64, *pairs*2)
	deadline := time.Now().Add(*duration)

	var powers []float64
	frames, ffts, shortReads := 0, 0, 0
	interrupted := false

measure:
	for time.Now().Before(deadline) {
		select {
		case sig := <-interrupt:
			fmt.Printf("\ninterrupted (%v) — reporting the partial window\n",
				sig)
			interrupted = true
			break measure
		default:
		}
		n, err := dev.ReadIQ(buf)
		if err != nil {
			fatal("read IQ", err)
		}
		frames++
		if n != len(buf) {
			shortReads++
			continue
		}
		if frames%*stride != 0 {
			continue
		}
		for i, v := range buf {
			iq[i] = float64(v) / 32768.0
		}
		res, err := dsp.ComputeIQFFT(iq, uint32(*rate))
		if err != nil || res == nil {
			continue
		}
		peak, _, ok := PeakPowerDB(res.PowerDB, res.Frequencies, dcGuardHz)
		if !ok {
			continue
		}
		powers = append(powers, peak)
		ffts++
	}

	report(*freq, *expected, *gain, applied, fullScaleDB(*pairs), interrupted,
		frames, ffts, shortReads, powers)
}

// fullScaleDB is the strongest-bin power_db a full-scale sinusoid
// produces through the §5.2/§5.4 scale (iq normalized to ±1, N-point
// wrapped FFT): 20·log10(N/2) ≈ 54.2 dB for 1024-pair frames.
// Advisories are keyed to this, not to 0 dB.
func fullScaleDB(pairs int) float64 { return 20 * math.Log10(float64(pairs)/2) }

// powerAdvisories returns the measurement-quality lines printed after
// a run: clipping risk when the strongest bin sits within 3 dB of
// full scale, instability when peak power varied > 3 dB, and a note
// when the mean is below the §5.4 −60 dB emission threshold.
func powerAdvisories(mean, sd, fullScale float64) []string {
	var out []string
	if mean > fullScale-3 {
		out = append(out,
			"WARNING: mean peak near full scale — likely clipping; "+
				"reduce -gain and re-run")
	}
	if sd > 3 {
		out = append(out,
			"WARNING: peak power varied > 3 dB — the reference moved "+
				"or the front end is overloaded; re-run with more distance "+
				"or less gain")
	}
	if mean <= -60 {
		out = append(out,
			"NOTE: mean peak is below the -60 dB peak threshold "+
				"(§5.4); the live pipeline would not emit this signal")
	}
	return out
}

// report summarizes the measurement window and prints the implied
// §5.6 offset as a ready-to-paste YAML line. The offset math uses the
// tuner-applied gain (requested may snap by up to a step), matching
// the applied_gain_db the live pipeline reports.
func report(freq, expected, requested, applied, fullScale float64, interrupted bool,
	frames, ffts, shortReads int, powers []float64) {

	if len(powers) == 0 {
		fmt.Fprintln(os.Stderr,
			"rtl-calibrate: no complete frames measured — check the "+
				"antenna/connection and that no other program holds the dongle")
		os.Exit(1)
	}

	mean := Mean(powers)
	sd := StdDev(powers)
	offset := ImpliedOffset(expected, mean, applied)

	stop := ""
	if interrupted {
		stop = " (early stop)"
	}
	fmt.Printf("measured: %d frames read, %d FFT frames, %d short reads%s\n",
		frames, ffts, shortReads, stop)
	fmt.Printf("peak power_db: mean %.2f dB, stddev %.2f dB over %d frames\n",
		mean, sd, len(powers))
	fmt.Println()

	for _, line := range powerAdvisories(mean, sd, fullScale) {
		fmt.Println(line)
	}

	fmt.Println("§5.6: power_dbm = power_db − applied_gain_db + " +
		"calibration_offset_db")
	fmt.Printf("implied offset = expected − mean + applied gain = "+
		"%.1f − %.2f + %.1f = %.2f dB\n", expected, mean, applied, offset)
	fmt.Println()
	fmt.Println("Paste into this device's sdrs[] entry in " +
		"config/sdr-capture.yaml:")
	fmt.Println()
	fmt.Printf("    calibration_offset_db: %.2f\n", offset)
	fmt.Println()
	fmt.Printf("Valid at this gain only — keep default_gain at %.1f dB in "+
		"the capture config (the tuner applies the same nearest step) "+
		"and re-run if it changes. On-air method accuracy ±6–10 dB "+
		"(docs/HARDWARE.md §6).\n", requested)
}

func fatal(stage string, err error) {
	fmt.Fprintf(os.Stderr, "rtl-calibrate: %s: %v\n", stage, err)
	os.Exit(1)
}
