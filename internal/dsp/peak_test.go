package dsp

import (
	"math"
	"testing"
)

// makeSpectrum builds a synthetic FFTResult with a flat noise floor and
// a single Gaussian-shaped peak at the specified bin.
func makeSpectrum(nfft int, sampleRate uint32, peakBin int, peakDB float64, noiseDB float64) *FFTResult {
	half := nfft / 2
	freqs := make([]float64, half)
	mags := make([]float64, half)
	power := make([]float64, half)
	df := float64(sampleRate) / float64(nfft)

	for i := 0; i < half; i++ {
		freqs[i] = float64(i) * df
		power[i] = noiseDB
	}

	// Add a triangular peak centered on peakBin (width ~10 bins)
	width := 10
	for d := -width; d <= width; d++ {
		idx := peakBin + d
		if idx < 0 || idx >= half {
			continue
		}
		triang := 1.0 - float64(absInt(d))/float64(width+1)
		power[idx] = noiseDB + triang*(peakDB-noiseDB)
	}
	mags = make([]float64, half)
	for i := range power {
		mags[i] = math.Pow(10, power[i]/10)
	}

	return &FFTResult{
		Frequencies: freqs,
		Magnitudes:  mags,
		PowerDB:     power,
		SampleRate:  sampleRate,
	}
}

func TestPeakDetector_FindsSinglePeak(t *testing.T) {
	nfft := 1024
	sr := uint32(48000)
	peakBin := 100

	result := makeSpectrum(nfft, sr, peakBin, 20.0, -40.0)
	pd := NewPeakDetector()
	peaks := pd.Detect(result)

	if len(peaks) == 0 {
		t.Fatal("expected at least 1 peak, got 0")
	}
	if len(peaks) > 3 {
		t.Fatalf("expected 1-3 peaks (near peakBin), got %d", len(peaks))
	}

	// The strongest peak should be at or very near peakBin
	best := peaks[0]
	for _, p := range peaks {
		if p.PowerDB > best.PowerDB {
			best = p
		}
	}
	if absInt(best.Index-peakBin) > 2 {
		t.Errorf("best peak at bin %d, expected near %d", best.Index, peakBin)
	}

	// Frequency should be correct
	df := float64(sr) / float64(nfft)
	expectedFreq := float64(peakBin) * df
	if math.Abs(best.FreqHz-expectedFreq) > df {
		t.Errorf("peak freq = %.1f Hz, expected ~%.1f Hz", best.FreqHz, expectedFreq)
	}

	// Bandwidth: a steep triangular peak drops below -3 dB within one
	// bin, so the (train.py-mirrored) walk may legitimately yield 0;
	// flat-top widths are covered in TestPeakDetector_Bandwidth.
	if best.Bandwidth < 0 {
		t.Errorf("bandwidth should be non-negative, got %f", best.Bandwidth)
	}
}

// TestPeakDetector_Bandwidth checks the -3 dB walk on a flat-top peak:
// 11 bins at 20 dB with a 21 dB center bin. The walk must span the full
// flat top (10 bins) and stop at the edges.
func TestPeakDetector_Bandwidth(t *testing.T) {
	nfft := 1024
	sr := uint32(48000)
	result := makeSpectrum(nfft, sr, 100, 20.0, -40.0)
	for i := 95; i <= 105; i++ {
		result.PowerDB[i] = 20.0
	}
	result.PowerDB[100] = 21.0 // strict local maximum on the flat top

	pd := NewPeakDetector()
	peaks := pd.Detect(result)
	if len(peaks) == 0 {
		t.Fatal("expected a peak, got 0")
	}
	best := peaks[0]
	if best.Index != 100 {
		t.Fatalf("peak at bin %d, expected 100", best.Index)
	}
	df := float64(sr) / float64(nfft)
	want := 10 * df // bins 95..105 inclusive
	if math.Abs(best.Bandwidth-want) > 1e-9 {
		t.Errorf("bandwidth = %f Hz, expected %f Hz", best.Bandwidth, want)
	}
}

func TestPeakDetector_ThresholdFiltersOut(t *testing.T) {
	nfft := 1024
	sr := uint32(48000)

	// Peak at -70 dB, default threshold is -60 dB → should be filtered
	result := makeSpectrum(nfft, sr, 100, -70.0, -80.0)
	pd := NewPeakDetector()
	peaks := pd.Detect(result)

	if len(peaks) != 0 {
		t.Errorf("expected 0 peaks (below threshold), got %d", len(peaks))
	}
}

func TestPeakDetector_TopN(t *testing.T) {
	nfft := 1024
	sr := uint32(48000)

	// Create many peaks
	power := make([]float64, nfft/2)
	freqs := make([]float64, nfft/2)
	df := float64(sr) / float64(nfft)
	for i := range power {
		freqs[i] = float64(i) * df
		power[i] = -50
		// Place peaks every 20 bins
		if i%20 == 10 {
			power[i] = 0
			power[i-1] = -10
			power[i+1] = -10
		}
	}
	result := &FFTResult{Frequencies: freqs, Magnitudes: make([]float64, nfft/2), PowerDB: power, SampleRate: sr}

	pd := NewPeakDetector()
	pd.TopN = 5
	peaks := pd.Detect(result)

	if len(peaks) != 5 {
		t.Errorf("expected 5 peaks (TopN=5), got %d", len(peaks))
	}
}

func TestPeakDetector_MinSpacing(t *testing.T) {
	nfft := 1024
	sr := uint32(48000)

	// Two peaks only 5 bins apart — with MinSpacing=10, only one should be found
	result := makeSpectrum(nfft, sr, 100, 10.0, -40.0)
	// Add a second peak at bin 105
	result.PowerDB[104] = -40
	result.PowerDB[105] = 8
	result.PowerDB[106] = -40

	pd := NewPeakDetector()
	pd.MinSpacing = 10
	peaks := pd.Detect(result)

	// Only one peak should be detected since they're within MinSpacing
	if len(peaks) > 1 {
		t.Errorf("expected 1 peak (min spacing enforced), got %d", len(peaks))
	}
}

// TestPeakDetector_StrongPeakNotCrowdedOut guards against the classic
// scan-in-bin-order truncation: with many low-bin noise peaks above the
// threshold, a strong signal at a high bin must still be returned (and
// ranked first) instead of being cut off by TopN.
func TestPeakDetector_StrongPeakNotCrowdedOut(t *testing.T) {
	nfft := 1024
	sr := uint32(48000)
	half := nfft / 2
	power := make([]float64, half)
	freqs := make([]float64, half)
	df := float64(sr) / float64(nfft)
	for i := range power {
		freqs[i] = float64(i) * df
		power[i] = -70
	}
	// 20 low-bin noise peaks above the -60 dB default threshold.
	for i := 10; i < 400; i += 20 {
		power[i-1], power[i], power[i+1] = -65, -55, -65
	}
	// One strong signal far above them, at a high bin offset.
	power[479], power[480], power[481] = -10, 10, -10
	result := &FFTResult{Frequencies: freqs, Magnitudes: make([]float64, half), PowerDB: power, SampleRate: sr}

	pd := NewPeakDetector()
	pd.TopN = 20
	peaks := pd.Detect(result)
	if len(peaks) == 0 {
		t.Fatal("expected peaks, got 0")
	}
	if peaks[0].Index != 480 {
		t.Errorf("strongest peak at bin %d, expected 480 (strong signal must not be crowded out)", peaks[0].Index)
	}
}

func TestPeakDetector_NilResult(t *testing.T) {
	pd := NewPeakDetector()
	peaks := pd.Detect(nil)
	if peaks != nil {
		t.Errorf("expected nil for nil input, got %v", peaks)
	}
}

func TestPeakDetector_ShortResult(t *testing.T) {
	pd := NewPeakDetector()
	result := &FFTResult{
		Frequencies: []float64{0, 1},
		Magnitudes:  []float64{0, 1},
		PowerDB:     []float64{0, 1},
		SampleRate:  48000,
	}
	peaks := pd.Detect(result)
	if peaks != nil {
		t.Errorf("expected nil for short input, got %v", peaks)
	}
}

func TestDetectNoiseFloor(t *testing.T) {
	// All same value
	power := make([]float64, 100)
	for i := range power {
		power[i] = -50
	}
	nf := DetectNoiseFloor(power)
	if nf != -50 {
		t.Errorf("noise floor = %f, expected -50", nf)
	}

	// Empty
	if nf := DetectNoiseFloor(nil); nf != 0 {
		t.Errorf("noise floor for empty = %f, expected 0", nf)
	}
}

func TestSNR(t *testing.T) {
	p := Power{FreqHz: 1000, PowerDB: 20}
	nf := -40.0
	snr := SNR(p, nf)
	if snr != 60 {
		t.Errorf("SNR = %f, expected 60", snr)
	}
}