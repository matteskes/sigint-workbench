package record

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sigint-workbench/internal/audio"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/sdr"
)

// Config configures the recorder (mirrors config/recorder.yaml).
type Config struct {
	Dir           string        // recordings directory
	MaxAgeDays    int           // file retention (§11: 30 days)
	MaxSizeGB     float64       // total recordings size cap (§11: 50 GB)
	PollInterval  time.Duration // active-signal poll period
	CloseSilence  time.Duration // idle time before a session finalizes
	MaxConcurrent int           // simultaneous session cap
	Streamer      *Streamer     // live audio mux (§10.4); nil disables
}

// Recorder tracks active signals and records the ones that appear in
// the shared IQ stream (D1, §10.2).
type Recorder struct {
	cfg      Config
	db       *db.DB
	registry *audio.Registry
	streamer *Streamer

	mu       sync.Mutex
	sessions map[string]*Session // signal ID -> open session
}

// NewRecorder creates a recorder. database may be nil (files only).
func NewRecorder(cfg Config, database *db.DB) *Recorder {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.CloseSilence <= 0 {
		cfg.CloseSilence = 10 * time.Second
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 8
	}
	if cfg.Dir == "" {
		cfg.Dir = "./recordings"
	}
	return &Recorder{
		cfg:      cfg,
		db:       database,
		registry: audio.DefaultRegistry(),
		streamer: cfg.Streamer,
		sessions: make(map[string]*Session),
	}
}

// inBand reports whether the signal frequency lies inside the frame's
// tunable band (center ± sample rate/2).
func inBand(sig db.Signal, frame *sdr.IQFrame) bool {
	if frame.SampleRate == 0 {
		return false
	}
	half := uint64(frame.SampleRate) / 2
	return sig.FreqHz >= frame.FreqHz-half && sig.FreqHz <= frame.FreqHz+half
}

// ObserveFrame feeds one IQ frame to every open session in band and
// opens sessions for tracked signals that just came into the band.
// tracked is the caller's active-signal snapshot (from SyncSignals).
func (r *Recorder) ObserveFrame(frame *sdr.IQFrame, tracked []db.Signal) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, sig := range tracked {
		if !inBand(sig, frame) {
			continue
		}
		sess, open := r.sessions[sig.ID]
		if !open {
			if len(r.sessions) >= r.cfg.MaxConcurrent {
				continue // at capacity; strongest-first compaction is future work (§10.2)
			}
			var err error
			sess, err = newSession(r.registry, r.cfg.Dir, sig, now)
			if err != nil {
				continue // unsupported modulation etc. — skip quietly
			}
			r.sessions[sig.ID] = sess
		}
		offset := float64(sig.FreqHz) - float64(frame.FreqHz)
		aud, err := sess.Feed(frame, offset)
		if err != nil {
			r.discardLocked(sig.ID, sess)
			continue
		}
		if r.streamer != nil {
			r.streamer.Feed(sig, aud)
		}
	}
}

// CloseIdle finalizes sessions idle for longer than the silence
// hysteresis and persists their recording rows. Empty recordings are
// discarded. Returns the number of recordings finalized.
func (r *Recorder) CloseIdle(now time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	var done []*Session
	for id, sess := range r.sessions {
		if now.Sub(sess.lastFeed) >= r.cfg.CloseSilence {
			done = append(done, sess)
			delete(r.sessions, id)
		}
	}
	for _, sess := range done {
		r.finalizeLocked(sess)
	}
	return len(done)
}

// PurgeFiles applies file retention (§11): deletes WAVs older than
// MaxAgeDays, then — while the directory exceeds MaxSizeGB — the
// oldest files. For every deleted file, remove(path) is invoked so
// the caller can delete the matching DB row.
func (r *Recorder) PurgeFiles(now time.Time, remove func(path string)) {
	entries, err := os.ReadDir(r.cfg.Dir)
	if err != nil {
		return
	}
	type meta struct {
		path  string
		mtime time.Time
		size  int64
	}
	var wavs []meta
	var total int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".wav" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		wavs = append(wavs, meta{filepath.Join(r.cfg.Dir, e.Name()), info.ModTime(), info.Size()})
		total += info.Size()
	}

	var removeSet []string
	if r.cfg.MaxAgeDays > 0 {
		maxAge := time.Duration(r.cfg.MaxAgeDays) * 24 * time.Hour
		var kept []meta
		for _, m := range wavs {
			if now.Sub(m.mtime) > maxAge {
				removeSet = append(removeSet, m.path)
				total -= m.size
				continue
			}
			kept = append(kept, m)
		}
		wavs = kept
	}
	if r.cfg.MaxSizeGB > 0 {
		budget := int64(r.cfg.MaxSizeGB * float64(1<<30))
		for _, m := range wavs { // entries are ReadDir-ordered ≈ name order
			if total <= budget {
				break
			}
			removeSet = append(removeSet, m.path)
			total -= m.size
		}
	}
	for _, path := range removeSet {
		if err := os.Remove(path); err == nil && remove != nil {
			remove(path)
		}
	}
}

// OpenSessions reports how many sessions are currently open.
func (r *Recorder) OpenSessions() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

// Levels returns a snapshot of the latest coarse audio level per open
// session (§10.6). Finalized/discarded sessions drop out of the map,
// so their level events simply stop.
func (r *Recorder) Levels() map[string]float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]float64, len(r.sessions))
	for id, sess := range r.sessions {
		out[id] = sess.level
	}
	return out
}

// Close finalizes every open session immediately.
func (r *Recorder) Close() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for id, sess := range r.sessions {
		delete(r.sessions, id)
		r.finalizeLocked(sess)
		n++
	}
	return n
}

// discardLocked removes a broken session without recording it.
func (r *Recorder) discardLocked(id string, sess *Session) {
	delete(r.sessions, id)
	r.endStreamLocked(id)
	sess.abandon()
}

// endStreamLocked tears down the live stream of a finalized or
// discarded session (no-op without a streamer).
func (r *Recorder) endStreamLocked(id string) {
	if r.streamer != nil {
		r.streamer.CloseStream(id)
	}
}

// finalizeLocked persists a finalized session's recording row.
func (r *Recorder) finalizeLocked(sess *Session) {
	r.endStreamLocked(sess.Signal.ID) // the live stream ends with the session (§10.4.4)
	rec, err := sess.finalize()
	if err != nil {
		sess.abandon()
		return
	}
	if rec.SizeBytes <= 0 {
		sess.abandon() // a recording with zero data bytes carries no audio (§10.2)
		return
	}
	if r.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := r.db.InsertRecording(ctx, &rec); err != nil {
			// Keep the file; the row can be reconciled later.
		}
	}
}
