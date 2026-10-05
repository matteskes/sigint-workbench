// §18 spectrum tap: a read-only side-tap that decimates each assembled
// FFT record (§5.7) into a spectrum.frame event for the dashboard
// spectrum/waterfall. Detection is untouched — the tap runs after peak
// detection, reads only the FFT result, and emits through the
// publisher's drop-never-block queue (§18.1).
package main

import (
	"sync"
	"time"

	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
)

// spectrumFrame is the §18.2 spectrum.frame payload (camelCase per
// §12.7). db[0] is the lowest-frequency bin of the span (−fs/2); db
// carries the §5.6 uncalibrated relative-dBFS domain — the display
// axis is "dB (rel.)" per the §18.2 honesty note.
type spectrumFrame struct {
	SDRID      string    `json:"sdrId"`
	FreqHz     uint64    `json:"freqHz"`
	SampleRate uint32    `json:"sampleRate"`
	T          time.Time `json:"t"`
	Bins       int       `json:"bins"`
	Df         float64   `json:"df"`
	DB         []float64 `json:"db"`
}

// spectrumTap rate-caps and emits spectrum.frame events per SDR.
// emit is wired to (*publisher).queue; a nil emit disables the tap.
type spectrumTap struct {
	bins        int           // decimated bin count (§18.4)
	minInterval time.Duration // 1/rate_hz per SDR (§18.1 pacing)
	emit        func(typ string, payload interface{})

	mu   sync.Mutex
	last map[string]time.Time // last emit per SDR
}

// newSpectrumTap builds a tap emitting at most rateHz frames/s per SDR,
// decimated to bins bins. Non-positive rateHz/bins take the §18.4
// defaults (5 Hz, 256); a nil emit returns nil (tap disabled).
func newSpectrumTap(bins int, rateHz float64, emit func(string, interface{})) *spectrumTap {
	if emit == nil {
		return nil
	}
	if bins <= 0 {
		bins = 256
	}
	if rateHz <= 0 {
		rateHz = 5
	}
	return &spectrumTap{
		bins:        bins,
		minInterval: time.Duration(float64(time.Second) / rateHz),
		emit:        emit,
		last:        make(map[string]time.Time),
	}
}

// observe emits one decimated spectrum.frame for an assembled FFT
// record. The per-SDR rate check happens before any allocation, so a
// suppressed record costs the DSP hot path nothing; enabled=false is
// modeled by the tap being nil, which also allocates nothing (§18.1).
func (t *spectrumTap) observe(frame *sdr.IQFrame, result *dsp.FFTResult) {
	if t == nil || frame == nil || result == nil || len(result.PowerDB) == 0 {
		return
	}
	now := time.Now()
	t.mu.Lock()
	if last, ok := t.last[frame.SDRID]; ok && now.Sub(last) < t.minInterval {
		t.mu.Unlock()
		return
	}
	t.last[frame.SDRID] = now
	t.mu.Unlock()

	// The only hot-path allocation §18.1 allows: the decimated bins.
	db := dsp.MaxPoolWrapped(result.PowerDB, t.bins)
	if db == nil {
		return
	}
	t.emit("spectrum.frame", spectrumFrame{
		SDRID:      frame.SDRID,
		FreqHz:     frame.FreqHz,
		SampleRate: frame.SampleRate,
		T:          frame.Timestamp,
		Bins:       len(db),
		Df:         result.BinSpacing,
		DB:         db,
	})
}
