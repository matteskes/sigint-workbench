// Command smoke-frames streams synthetic CW or WFM IQ frames over UDP to a
// running signal-processor, so scripts/smoke-test.sh can exercise the real
// binary end to end (IQ -> FFT -> peak -> features -> ONNX -> log).
//
// Frames are built the way models/train.py build_dataset and the Go E2E tests
// do: a tone (pure for CW, piecewise-constant deviation for WFM) at an offset
// from the carrier, scaled so its strongest FFT bin sits 25 dB above the 1e-6
// per-sample noise floor *at the frame's own size* (the ONNX model was trained
// in the 18-30 dB SNR range), plus Gaussian noise. Frames are sized to the
// 1024-pair UDP wire limit, so the processor's 1024-point FFT still sees a
// 25 dB-SNR signal.
//
// Usage:
//
//	smoke-frames -mod cw  -addr 127.0.0.1:PORT -count 5
//	smoke-frames -mod wfm -addr 127.0.0.1:PORT -count 5
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net"
	"os"
	"time"

	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
)

const (
	// sampleRate matches models/train.py SAMPLE_RATE (fs = 4.096 MHz,
	// df = 1 kHz/bin at the 4096 training NFFT).
	sampleRate = 4_096_000
	// snrDB keeps the synthetic tone inside the 18-30 dB training range.
	snrDB = 25.0
)

func main() {
	mod := flag.String("mod", "cw", "modulation: cw or wfm")
	addr := flag.String("addr", "127.0.0.1:9010", "signal-processor UDP address")
	count := flag.Int("count", 5, "number of frames to send")
	nfft := flag.Int("nfft", 1024, "IQ pairs per frame (<= 1024, the UDP wire limit)")
	id := flag.String("id", "smoke", "SDR id written into the frame header")
	offsetFlag := flag.Float64("offset", 0, "override tone offset in Hz (negative = below center; 0 = mod default)")
	flag.Parse()

	var (
		centerHz uint64
		offsetHz float64
		devHz    float64
	)
	switch *mod {
	case "cw":
		// 1.5 MHz offset on a 14.5 MHz frame -> 16.0000 MHz peak.
		centerHz, offsetHz, devHz = 14_500_000, 1.5e6, 0
	case "wfm":
		// 800 kHz offset on a 100 MHz frame, ~140 kHz Carson width. 100.8 MHz
		// is inside the WFM band (88-108 MHz) and bin 800 is in the training
		// carrier range (train.py picks bins 250..2047-bw).
		centerHz, offsetHz, devHz = 100_000_000, 800_000, 70_000
	default:
		fmt.Fprintf(os.Stderr, "unknown -mod %q (want cw or wfm)\n", *mod)
		flag.Usage()
		os.Exit(2)
	}
	if *offsetFlag != 0 {
		offsetHz = *offsetFlag // §17.3: allow a below-center (negative) tone
	}

	frame, err := buildFrame(centerHz, offsetHz, devHz, *nfft, *id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build frame: %v\n", err)
		os.Exit(1)
	}
	payload, err := frame.Encode()
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode frame: %v\n", err)
		os.Exit(1)
	}

	dst, err := net.ResolveUDPAddr("udp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve %s: %v\n", *addr, err)
		os.Exit(1)
	}
	conn, err := net.DialUDP("udp", nil, dst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", *addr, err)
		os.Exit(1)
	}
	defer conn.Close()

	for i := 0; i < *count; i++ {
		if _, err := conn.Write(payload); err != nil {
			fmt.Fprintf(os.Stderr, "send: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(50 * time.Millisecond)
	}

	peakHz := int64(centerHz) + int64(offsetHz)
	if peakHz < 0 {
		peakHz = 0
	}
	fmt.Printf("sent %d %s frame(s) -> %s (expect peak ~%.4f MHz)\n",
		*count, *mod, *addr, float64(peakHz)/1e6)
}

// buildFrame builds a synthetic IQFrame (tone + 1e-6 per-sample noise power),
// scaled so its strongest FFT bin is snrDB above the noise floor at the frame's
// own size. This mirrors models/train.py build_dataset and the Go E2E helpers.
func buildFrame(centerHz uint64, offsetHz, devHz float64, nfft int, sdrID string) (*sdr.IQFrame, error) {
	const sigma = 7.0711e-4 // sqrt(1e-6 / 2) per I or Q sample
	rng := rand.New(rand.NewSource(1))

	// Piecewise-constant-frequency tone (CW when devHz == 0, else FM).
	ph, step := 0.0, 2*math.Pi*offsetHz/sampleRate
	hopLen := nfft / 16
	tone := make([]float64, nfft*2)
	for i := 0; i < nfft; i++ {
		if devHz > 0 && i%hopLen == 0 {
			step = 2 * math.Pi * (offsetHz + (2*rng.Float64()-1)*devHz) / sampleRate
		}
		tone[2*i] = math.Cos(ph)
		tone[2*i+1] = math.Sin(ph)
		ph += step
	}

	// Scale the tone so its peak bin is snrDB above the noise floor (the
	// unnormalized FFT power floor is 10*log10(nfft * 1e-6)).
	res, err := dsp.ComputeIQFFT(tone, sampleRate)
	if err != nil || res == nil {
		return nil, fmt.Errorf("tone FFT: %w", err)
	}
	peak := math.Inf(-1)
	for _, p := range res.PowerDB {
		if p > peak {
			peak = p
		}
	}
	target := 10*math.Log10(float64(nfft)*1e-6) + snrDB
	gain := math.Pow(10, (target-peak)/20)

	samples := make([]int16, nfft*2)
	for i := range tone {
		v := gain*tone[i] + sigma*rng.NormFloat64()
		if v > 1 {
			v = 1
		}
		if v < -1 {
			v = -1
		}
		samples[i] = int16(math.Round(v * 32767))
	}

	return &sdr.IQFrame{
		SDRID:      sdrID,
		FreqHz:     centerHz,
		SampleRate: sampleRate,
		Timestamp:  time.Now(),
		Samples:    samples,
	}, nil
}
