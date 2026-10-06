package record

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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
	IQEnabled     bool          // raw-IQ side recording (§10.5, iq.enabled)
	MaxDuration   time.Duration // recording cap (§11.1, iq.max_duration_s); <0 uncapped
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
	if cfg.MaxDuration == 0 {
		cfg.MaxDuration = 300 * time.Second // §16.5 iq.max_duration_s default
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
			sess, err = newSession(r.registry, r.cfg.Dir, sig, now, r.cfg.IQEnabled)
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
// hysteresis — or running longer than the §11.1 max-duration cap —
// and persists their recording rows. Empty recordings are discarded.
// Returns the number of recordings finalized.
func (r *Recorder) CloseIdle(now time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	var done []*Session
	for id, sess := range r.sessions {
		silence := now.Sub(sess.lastFeed) >= r.cfg.CloseSilence
		capped := r.cfg.MaxDuration > 0 && now.Sub(sess.start) >= r.cfg.MaxDuration
		if silence || capped {
			done = append(done, sess)
			delete(r.sessions, id)
		}
	}
	for _, sess := range done {
		r.finalizeLocked(sess)
	}
	return len(done)
}

// PurgeFiles applies file retention (§11): deletes WAV/IQ files
// (§10.5) older than MaxAgeDays, then — while the directory exceeds
// MaxSizeGB — the oldest files. The policy itself lives in SelectPurge
// (this used to reimplement it inline and evict size-cap victims in
// ReadDir name order — A10); files are sorted oldest-first (mtime)
// before selection. For every deleted file, remove(path) is invoked so
// the caller can delete the matching DB row.
func (r *Recorder) PurgeFiles(now time.Time, remove func(path string)) {
	entries, err := os.ReadDir(r.cfg.Dir)
	if err != nil {
		return
	}
	var recs []RecordingMeta
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".wav" && ext != ".iq") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		recs = append(recs, RecordingMeta{
			ID:        e.Name(),
			FilePath:  filepath.Join(r.cfg.Dir, e.Name()),
			StartTime: info.ModTime(), // mtime ≈ session finalize time
			SizeBytes: info.Size(),
		})
	}

	// SelectPurge is order-sensitive (it never reorders): oldest first
	// so the size-cap phase evicts the oldest recordings.
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].StartTime.Before(recs[j].StartTime)
	})

	var maxAge time.Duration
	if r.cfg.MaxAgeDays > 0 {
		maxAge = time.Duration(r.cfg.MaxAgeDays) * 24 * time.Hour
	}
	var maxSize int64
	if r.cfg.MaxSizeGB > 0 {
		maxSize = int64(r.cfg.MaxSizeGB * float64(1<<30))
	}
	for _, m := range SelectPurge(recs, now, maxAge, maxSize) {
		if err := os.Remove(m.FilePath); err == nil && remove != nil {
			remove(m.FilePath)
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

// finalizeLocked persists a finalized session's recording rows.
func (r *Recorder) finalizeLocked(sess *Session) {
	r.endStreamLocked(sess.Signal.ID) // the live stream ends with the session (§10.4.4)
	rows, err := sess.finalize()
	if err != nil {
		sess.abandon()
		return
	}
	if len(rows) == 0 {
		sess.abandon() // no file carried data bytes (§10.2)
		return
	}
	if r.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for i := range rows {
			if err := r.db.InsertRecording(ctx, &rows[i]); err != nil {
				// Keep the file; the row can be reconciled later.
			}
		}
	}
}
