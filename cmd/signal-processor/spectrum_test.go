package main

import (
	"encoding/json"
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"sigint-workbench/internal/classify"
	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
)

// toneSamples builds n interleaved IQ pairs carrying a unit-ish
// complex exponential at the given baseband offset (negative = below
// center), the same shape smoke-frames builds for its synthetic CW.
func toneSamples(n int, sampleRate uint32, offsetHz float64) []int16 {
	samples := make([]int16, 2*n)
	w := 2 * math.Pi * offsetHz / float64(sampleRate)
	for i := 0; i < n; i++ {
		ph := w * float64(i)
		samples[2*i] = int16(8000 * math.Cos(ph))
		samples[2*i+1] = int16(8000 * math.Sin(ph))
	}
	return samples
}

// iqFloat converts int16 IQ pairs to the pipeline's float64 form.
func iqFloat(samples []int16) []float64 {
	iq := make([]float64, len(samples))
	for i, v := range samples {
		iq[i] = float64(v) / 32768.0
	}
	return iq
}

func TestSpectrumTapPayloadValidity(t *testing.T) {
	const (
		n  = 4096
		fs = uint32(4_096_000)
	)
	frame := &sdr.IQFrame{
		SDRID: "sim0", FreqHz: 100_000_000, SampleRate: fs,
		Timestamp: time.Unix(1_700_000_000, 0).UTC(),
		Samples:   toneSamples(n, fs, -100_000), // tone at −100 kHz
	}
	result, err := dsp.ComputeIQFFT(iqFloat(frame.Samples), fs)
	if err != nil || result == nil {
		t.Fatalf("ComputeIQFFT: %v", err)
	}

	var (
		typ     string
		payload spectrumFrame
		calls   int
	)
	tap := newSpectrumTap(256, 5, func(t2 string, p interface{}) {
		typ = t2
		payload = p.(spectrumFrame)
		calls++
	})
	tap.observe(frame, result)

	if calls != 1 {
		t.Fatalf("emit calls = %d, want 1", calls)
	}
	if typ != "spectrum.frame" {
		t.Fatalf("type = %q, want spectrum.frame", typ)
	}

	// §18.2 field contract.
	if payload.SDRID != frame.SDRID || payload.FreqHz != frame.FreqHz ||
		payload.SampleRate != frame.SampleRate {
		t.Fatalf("header fields = %+v, want sdr/freq/rate from the frame", payload)
	}
	if !payload.T.Equal(frame.Timestamp) {
		t.Fatalf("t = %v, want %v", payload.T, frame.Timestamp)
	}
	if payload.Bins != 256 || payload.Bins != len(payload.DB) {
		t.Fatalf("bins = %d, len(db) = %d, want 256/256", payload.Bins, len(payload.DB))
	}
	if payload.Df != float64(fs)/n {
		t.Fatalf("df = %v, want %v (sampleRate/fft.size)", payload.Df, float64(fs)/n)
	}

	// Ordering: a tone at −100 kHz (df = 1 kHz ⇒ wrapped bin 3996,
	// shifted k = 1948) must land in decimated group 1948/16 = 121 —
	// and nowhere near the top of the span.
	best := 0
	for i, v := range payload.DB {
		if v > payload.DB[best] {
			best = i
		}
	}
	if best != 121 {
		t.Fatalf("strongest decimated bin = %d, want 121 (−100 kHz tone)", best)
	}

	// §14.3 envelope validity: the payload must marshal to a JSON
	// object carrying every §18.2 key (publisher.queue does exactly
	// this marshal on the emit path).
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload is not a JSON object: %v", err)
	}
	for _, k := range []string{"sdrId", "freqHz", "sampleRate", "t", "bins", "df", "db"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("payload missing key %q", k)
		}
	}
}

func TestSpectrumTapRateCapPerSDR(t *testing.T) {
	var emits []string
	tap := newSpectrumTap(4, 5, func(_ string, _ interface{}) { emits = append(emits, "x") })
	tap.minInterval = 50 * time.Millisecond // deterministic test pacing

	res := &dsp.FFTResult{PowerDB: make([]float64, 8), BinSpacing: 1000, SampleRate: 8000}
	a := &sdr.IQFrame{SDRID: "a", FreqHz: 1, SampleRate: 8000}
	b := &sdr.IQFrame{SDRID: "b", FreqHz: 1, SampleRate: 8000}

	tap.observe(a, res) // first frame from a: emits
	tap.observe(a, res) // inside the interval: suppressed
	if len(emits) != 1 {
		t.Fatalf("emits = %d, want 1 (rate cap must suppress)", len(emits))
	}
	tap.observe(b, res) // other SDR: independent cap
	if len(emits) != 2 {
		t.Fatalf("emits = %d, want 2 (per-SDR cap)", len(emits))
	}
	time.Sleep(60 * time.Millisecond)
	tap.observe(a, res) // interval elapsed: emits again
	if len(emits) != 3 {
		t.Fatalf("emits = %d, want 3 (interval elapsed)", len(emits))
	}
}

func TestSpectrumTapDisabled(t *testing.T) {
	// enabled=false is modeled by a nil tap; observing through it must
	// be a no-op (zero events, zero allocation — §18.1).
	var nilTap *spectrumTap
	nilTap.observe(&sdr.IQFrame{SDRID: "a"}, &dsp.FFTResult{PowerDB: make([]float64, 8)})

	// A nil emit callback also disables the tap at construction.
	if tap := newSpectrumTap(4, 5, nil); tap != nil {
		t.Fatalf("newSpectrumTap with nil emit = %+v, want nil", tap)
	}
}

func TestProcessFrameTapDoesNotAlterDetection(t *testing.T) {
	// §18.1: the tap MUST NOT alter detection results. The same frame
	// processed with and without a live tap must yield identical
	// events.
	pd := &dsp.PeakDetector{ThresholdDB: -60, MinSpacing: 10, TopN: 20}
	rules := classify.NewRuleClassifier()
	frame := &sdr.IQFrame{
		SDRID: "sdr0", FreqHz: 10_000_000, SampleRate: 4_096_000,
		Samples: toneSamples(1024, 4_096_000, 500_000),
	}
	emits := 0
	tap := newSpectrumTap(16, 1000, func(string, interface{}) { emits++ })

	without := processFrame(frame, pd, rules, dsp.WindowRectangular, nil)
	with := processFrame(frame, pd, rules, dsp.WindowRectangular, tap)

	if len(without) == 0 {
		t.Fatal("no events from tone frame")
	}
	if !reflect.DeepEqual(without, with) {
		t.Fatalf("events differ with tap active:\nwithout: %+v\nwith:    %+v", without, with)
	}
	if emits != 1 {
		t.Fatalf("tap emits = %d, want 1", emits)
	}
}

func TestProcessFrameEmitsWithoutPeaks(t *testing.T) {
	// §18.1 placement guard: the tap runs after peak detection but
	// before the empty-spectrum early return, so a noise-only record
	// — no peaks above threshold — must still emit a waterfall row.
	rng := rand.New(rand.NewSource(42))
	frame := &sdr.IQFrame{
		SDRID: "sdr0", FreqHz: 10_000_000, SampleRate: 4_096_000,
		Samples: make([]int16, 2*1024),
	}
	for i := range frame.Samples {
		frame.Samples[i] = int16(rng.NormFloat64() * 50) // ≈ −26 dB bins
	}

	pd := &dsp.PeakDetector{ThresholdDB: 0, MinSpacing: 10, TopN: 20} // nothing clears 0 dB
	rules := classify.NewRuleClassifier()
	emits := 0
	tap := newSpectrumTap(16, 1000, func(string, interface{}) { emits++ })

	events := processFrame(frame, pd, rules, dsp.WindowRectangular, tap)
	if len(events) != 0 {
		t.Fatalf("noise produced %d events, want 0", len(events))
	}
	if emits != 1 {
		t.Fatalf("tap emits = %d, want 1 (noise-only record must still feed the waterfall)", emits)
	}
}
