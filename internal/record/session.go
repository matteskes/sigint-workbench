// Package record — in-band signal recording (SPEC D1, §10.2).
//
// The recorder consumes the shared IQ stream (iq-ingest fan-out) and,
// for every tracked signal that is inside the currently streamed
// band, frequency-shifts the signal to baseband, demodulates it with
// the audio registry, and appends the audio to a per-signal WAV
// session (plus a raw-IQ side file, §10.5, when iq.enabled). Sessions
// finalize after a silence hysteresis — or at the §11.1 max-duration
// cap — and land row(s) in the recordings table.
package record

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"sigint-workbench/internal/audio"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/dsp"
	"sigint-workbench/internal/sdr"
)

// Session is one in-progress recording of one signal. Not safe for
// concurrent use — the Recorder serializes access.
type Session struct {
	Signal db.Signal
	Demod  audio.Demodulator

	mixer    mixer // phase-continuous frequency shifter
	agc      *dsp.AGC
	file     *os.File
	wav      *audio.WAVWriter
	start    time.Time
	lastFeed time.Time
	path     string
	level    float64 // latest coarse level 0..1 (§10.6)

	iqFile    *os.File // §10.5 raw-IQ side file; nil when disabled
	iqPath    string
	iqBytes   int64  // raw-IQ payload bytes written
	iqRate    uint32 // capture sample rate (first frame)
	iqScratch []byte // reusable LE byte buffer (single writer)
}

// sessionFileBase builds the recording file name stem:
// <signalid8>-<yyyymmddThhmmss> (filesystem-safe, sortable). Callers
// append the format extension — .wav or .iq (§10.5).
func sessionFileBase(sig db.Signal, at time.Time) string {
	id := sig.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%s-%s", id, at.UTC().Format("20060102T150405"))
}

// newSession opens the WAV file — and, when iqEnabled, the §10.5
// raw-IQ side file — for sig under dir and returns the ready-to-feed
// session. The demodulator is resolved from the audio registry by
// (modulation, subType).
func newSession(reg *audio.Registry, dir string, sig db.Signal, at time.Time, iqEnabled bool) (*Session, error) {
	demod, err := reg.Get(sig.Modulation, sig.SubType)
	if err != nil {
		return nil, fmt.Errorf("record: no demodulator for %s/%s: %w", sig.Modulation, sig.SubType, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("record: mkdir: %w", err)
	}
	path := filepath.Join(dir, sessionFileBase(sig, at)+".wav")
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("record: create %s: %w", path, err)
	}
	wav, err := audio.NewWAVWriter(f, demod.AudioSampleRate())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("record: wav header: %w", err)
	}
	s := &Session{
		Signal:   sig,
		Demod:    demod,
		agc:      dsp.NewAGC(), // §10.6 metering: target 0.95, attack 1, release 50
		file:     f,
		wav:      wav,
		start:    at,
		lastFeed: at,
		path:     path,
	}
	if iqEnabled {
		s.iqPath = filepath.Join(dir, sessionFileBase(sig, at)+".iq")
		iqf, err := os.Create(s.iqPath)
		if err != nil {
			f.Close()
			os.Remove(path)
			return nil, fmt.Errorf("record: create %s: %w", s.iqPath, err)
		}
		s.iqFile = iqf
	}
	return s, nil
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
	if s.iqFile != nil {
		// §10.5: persist the shifted baseband IQ (pre-demod) as raw
		// int16 interleaved I/Q at the capture sample rate.
		if err := s.writeIQ(shifted, frame.SampleRate); err != nil {
			return nil, fmt.Errorf("record: write iq %s: %w", s.iqPath, err)
		}
	}
	aud, err := s.Demod.Demodulate(shifted, frame.SampleRate)
	if err != nil {
		return nil, fmt.Errorf("record: demodulate %s: %w", s.Signal.ID, err)
	}
	s.lastFeed = time.Now()
	s.level = audioLevel(s.agc, aud)
	return aud, s.wav.Write(aud)
}

// audioLevel meters one demodulated chunk: the §5.7 AGC (target 0.95,
// attack 1, release 50) runs on the demodulated float32 stream and the
// chunk's post-AGC RMS — clamped to [0,1] — is the coarse level
// published as audio.level (§10.6). The AGC instance lives on the
// session so gain continuity is kept across frames.
func audioLevel(agc *dsp.AGC, aud []float32) float64 {
	if len(aud) == 0 {
		return 0
	}
	buf := make([]float64, len(aud))
	for i, v := range aud {
		buf[i] = float64(v)
	}
	out := agc.Process(buf)
	var sum float64
	for _, v := range out {
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(len(out)))
	if rms < 0 {
		return 0
	}
	if rms > 1 {
		return 1
	}
	return rms
}

// Level returns the session's latest coarse audio level (§10.6).
func (s *Session) Level() float64 { return s.level }

// finalize closes the files and returns the recording rows to
// persist: the wav row, plus an iq row (§10.5) when raw IQ was
// captured. Rows whose file carries no data bytes are omitted and
// their file removed (§10.2: an empty recording carries no audio) —
// a session that produced nothing returns no rows at all.
func (s *Session) finalize() ([]db.Recording, error) {
	if err := s.wav.Close(); err != nil {
		s.file.Close()
		s.closeIQ()
		return nil, fmt.Errorf("record: finalize %s: %w", s.path, err)
	}
	if err := s.file.Close(); err != nil {
		s.closeIQ()
		return nil, fmt.Errorf("record: finalize %s: %w", s.path, err)
	}

	var rows []db.Recording
	if n := s.wav.DataBytes(); n > 0 {
		rows = append(rows, db.Recording{
			SignalID:    s.Signal.ID,
			StartTime:   s.start,
			EndTime:     s.lastFeed,
			DurationS:   s.lastFeed.Sub(s.start).Seconds(),
			SampleRate:  int32(s.Demod.AudioSampleRate()),
			CenterFreq:  s.Signal.FreqHz,
			BandwidthHz: s.Signal.BandwidthHz,
			FilePath:    s.path,
			FileFormat:  "wav",
			SizeBytes:   int64(n),
		})
	} else {
		os.Remove(s.path) // header-only WAV — not a recording (§10.2)
	}

	if s.iqPath != "" {
		if err := s.iqFile.Close(); err != nil {
			s.iqFile = nil
			return rows, fmt.Errorf("record: finalize %s: %w", s.iqPath, err)
		}
		s.iqFile = nil
		if s.iqBytes > 0 {
			rows = append(rows, db.Recording{
				SignalID:    s.Signal.ID,
				StartTime:   s.start,
				EndTime:     s.lastFeed,
				DurationS:   s.lastFeed.Sub(s.start).Seconds(),
				SampleRate:  int32(s.iqRate),
				CenterFreq:  s.Signal.FreqHz,
				BandwidthHz: s.Signal.BandwidthHz,
				FilePath:    s.iqPath,
				FileFormat:  "iq",
				SizeBytes:   s.iqBytes,
			})
		} else {
			os.Remove(s.iqPath) // empty IQ side file (§10.5)
		}
	}
	return rows, nil
}

// writeIQ appends one frame of raw baseband IQ (§10.5): int16
// interleaved I/Q, little-endian, headerless, at the capture sample
// rate. The byte buffer is reused across feeds (single writer).
func (s *Session) writeIQ(shifted []complex64, rate uint32) error {
	if s.iqRate == 0 {
		s.iqRate = rate // the iq row carries the capture rate (§10.5)
	}
	if cap(s.iqScratch) < len(shifted)*4 {
		s.iqScratch = make([]byte, len(shifted)*4)
	}
	buf := s.iqScratch[:len(shifted)*4]
	for i, v := range shifted {
		binary.LittleEndian.PutUint16(buf[i*4:], iqSample(real(v)))
		binary.LittleEndian.PutUint16(buf[i*4+2:], iqSample(imag(v)))
	}
	n, err := s.iqFile.Write(buf)
	s.iqBytes += int64(n)
	return err
}

// iqSample rescales one normalized sample ([-1, 1)) back to the int16
// capture scale, clamped.
func iqSample(v float32) uint16 {
	x := math.Round(float64(v) * 32768)
	switch {
	case x > 32767:
		x = 32767
	case x < -32768:
		x = -32768
	}
	return uint16(int16(x))
}

// closeIQ closes the raw-IQ side file if it is still open.
func (s *Session) closeIQ() {
	if s.iqFile != nil {
		s.iqFile.Close()
		s.iqFile = nil
	}
}

// abandon discards an unusable or empty session: the WAV and raw-IQ
// files (header-only, empty, or damaged) are removed rather than
// recorded (§10.2, §10.5).
func (s *Session) abandon() {
	s.wav.Close()
	s.file.Close()
	s.closeIQ()
	os.Remove(s.path)
	if s.iqPath != "" {
		os.Remove(s.iqPath)
	}
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
