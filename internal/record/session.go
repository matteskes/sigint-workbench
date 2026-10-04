// Package record — in-band signal recording (SPEC D1, §10.2).
//
// The recorder consumes the shared IQ stream (iq-ingest fan-out) and,
// for every tracked signal that is inside the currently streamed
// band, frequency-shifts the signal to baseband, demodulates it with
// the audio registry, and appends the audio to a per-signal WAV
// session. Sessions finalize after a silence hysteresis and land a
// row in the recordings table.
package record

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"sigint-workbench/internal/audio"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/sdr"
)

// Session is one in-progress recording of one signal. Not safe for
// concurrent use — the Recorder serializes access.
type Session struct {
	Signal db.Signal
	Demod  audio.Demodulator

	mixer    mixer // phase-continuous frequency shifter
	file     *os.File
	wav      *audio.WAVWriter
	start    time.Time
	lastFeed time.Time
	path     string
}

// sessionFileBase builds the recording file name:
// <signalid8>-<yyyymmddThhmmss>.wav (filesystem-safe, sortable).
func sessionFileBase(sig db.Signal, at time.Time) string {
	id := sig.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%s-%s.wav", id, at.UTC().Format("20060102T150405"))
}

// newSession opens the WAV file for sig under dir and returns the
// ready-to-feed session. The demodulator is resolved from the audio
// registry by (modulation, subType).
func newSession(reg *audio.Registry, dir string, sig db.Signal, at time.Time) (*Session, error) {
	demod, err := reg.Get(sig.Modulation, sig.SubType)
	if err != nil {
		return nil, fmt.Errorf("record: no demodulator for %s/%s: %w", sig.Modulation, sig.SubType, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("record: mkdir: %w", err)
	}
	path := filepath.Join(dir, sessionFileBase(sig, at))
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("record: create %s: %w", path, err)
	}
	wav, err := audio.NewWAVWriter(f, demod.AudioSampleRate())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("record: wav header: %w", err)
	}
	return &Session{
		Signal:   sig,
		Demod:    demod,
		file:     f,
		wav:      wav,
		start:    at,
		lastFeed: at,
		path:     path,
	}, nil
}

// Feed demodulates one IQ frame for this session's signal and appends
// the audio. offsetHz is the signal's offset from the frame center
// (positive = above center); the mixer keeps phase continuity across
// frames so FM does not click at frame boundaries. The demodulated
// audio is returned so the caller can also feed the live stream
// (§10.4) without re-shifting.
func (s *Session) Feed(frame *sdr.IQFrame, offsetHz float64) ([]float32, error) {
	iq := int16PairsToComplex(frame.Samples)
	shifted := s.mixer.shift(iq, offsetHz, frame.SampleRate)
	aud, err := s.Demod.Demodulate(shifted, frame.SampleRate)
	if err != nil {
		return nil, fmt.Errorf("record: demodulate %s: %w", s.Signal.ID, err)
	}
	s.lastFeed = time.Now()
	return aud, s.wav.Write(aud)
}

// finalize closes the WAV and returns the recording row to persist.
func (s *Session) finalize() (db.Recording, error) {
	rec := db.Recording{
		SignalID:    s.Signal.ID,
		StartTime:   s.start,
		EndTime:     s.lastFeed,
		DurationS:   s.lastFeed.Sub(s.start).Seconds(),
		SampleRate:  int32(s.Demod.AudioSampleRate()),
		CenterFreq:  s.Signal.FreqHz,
		BandwidthHz: s.Signal.BandwidthHz,
		FilePath:    s.path,
		FileFormat:  "wav",
		SizeBytes:   int64(s.wav.DataBytes()),
	}
	if err := s.wav.Close(); err != nil {
		s.file.Close()
		return rec, fmt.Errorf("record: finalize %s: %w", s.path, err)
	}
	return rec, s.file.Close()
}

// abandon discards an unusable or empty session: the WAV (header-only,
// or damaged) is removed rather than recorded (§10.2).
func (s *Session) abandon() {
	s.wav.Close()
	s.file.Close()
	os.Remove(s.path)
}

// int16PairsToComplex converts interleaved int16 I/Q to normalized
// complex64 ([-1, 1) scale).
func int16PairsToComplex(samples []int16) []complex64 {
	out := make([]complex64, 0, len(samples)/2)
	for i := 0; i+1 < len(samples); i += 2 {
		out = append(out, complex(
			float32(samples[i])/32768,
			float32(samples[i+1])/32768,
		))
	}
	return out
}

// mixer is a phase-continuous complex frequency shifter.
type mixer struct {
	phase float64 // radians, carried across feeds
}

// shift multiplies the complex baseband by exp(-j2π·offsetHz·t),
// centering a signal at +offsetHz on DC. Phase continuity is kept
// across calls even when offsetHz changes (scanner retunes).
func (m *mixer) shift(iq []complex64, offsetHz float64, rate uint32) []complex64 {
	if offsetHz == 0 {
		return iq
	}
	inc := -2 * math.Pi * offsetHz / float64(rate)
	out := make([]complex64, len(iq))
	for i, v := range iq {
		m.phase += inc
		if m.phase > math.Pi {
			m.phase -= 2 * math.Pi
		} else if m.phase < -math.Pi {
			m.phase += 2 * math.Pi
		}
		c := complex(math.Cos(m.phase), math.Sin(m.phase))
		out[i] = complex64(complex128(v) * c)
	}
	return out
}
