package record

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sigint-workbench/internal/audio"
	"sigint-workbench/internal/db"
	"sigint-workbench/internal/sdr"
)

func testSignal() db.Signal {
	return db.Signal{
		ID:          "11111111-1111-1111-1111-111111111111",
		FreqHz:      146_520_000,
		BandwidthHz: 12_500,
		Modulation:  "AM",
		Active:      true,
	}
}

func testFrame(centerHz uint64, n int) *sdr.IQFrame {
	samples := make([]int16, n*2)
	// A weak real tone so demodulators produce actual sample counts.
	for i := range n {
		v := int16(2000 * math.Sin(float64(i)*0.05))
		samples[i*2] = v
		samples[i*2+1] = 0
	}
	return &sdr.IQFrame{
		SDRID:      "rtlsdr-0",
		FreqHz:     centerHz,
		SampleRate: 2_400_000,
		Timestamp:  time.Now(),
		Samples:    samples,
	}
}

// TestAudioLevelFeed covers the §10.6 meter tap: an open session
// produces a bounded level, and finalizing the session removes it.
func TestAudioLevelFeed(t *testing.T) {
	rec := NewRecorder(Config{Dir: t.TempDir()}, nil)
	sig := testSignal()
	tracked := []db.Signal{sig}
	frame := testFrame(sig.FreqHz, 4096) // signal at frame center → in band

	for i := 0; i < 10; i++ {
		rec.ObserveFrame(frame, tracked)
	}

	lvl, ok := rec.Levels()[sig.ID]
	if !ok {
		t.Fatalf("no level entry for open session: %v", rec.Levels())
	}
	if lvl <= 0 || lvl > 1 {
		t.Fatalf("level = %v, want in (0, 1]", lvl)
	}

	// Closing the recorder finalizes every session; the level map
	// must empty out (events stop when demodulation stops).
	rec.Close()
	if remaining := rec.Levels(); len(remaining) != 0 {
		t.Fatalf("levels after close = %v, want empty", remaining)
	}
}

func TestMixerShiftsToneToDC(t *testing.T) {
	// A complex exponential at +100 kHz must land on DC after shifting
	// by -100 kHz: the samples become (mostly) real and constant.
	const rate = 2_400_000
	const tone = 100_000.0
	raw := make([]complex64, 2400)
	for i := range raw {
		ph := 2 * math.Pi * tone * float64(i) / rate
		raw[i] = complex64(complex(math.Cos(ph), math.Sin(ph)))
	}
	var m mixer
	// The tone sits at +100 kHz offset from the tuning center; the
	// mixer must shift it DOWN to DC. A residual constant phase
	// rotation is expected (and harmless for FM/AM demodulation);
	// what matters is that the output is a constant complex value.
	shifted := m.shift(raw, tone, rate)
	var sum complex128
	for _, v := range shifted {
		sum += complex128(v)
	}
	avg := sum / complex(float64(len(shifted)), 0)
	if mag := cmplx.Abs(avg); mag < 0.9 || mag > 1.1 {
		t.Fatalf("shifted tone |average| = %v, want ≈ 1.0", mag)
	}
	var variance float64
	for _, v := range shifted {
		variance += cmplx.Abs(complex128(v) - avg)
	}
	variance /= float64(len(shifted))
	if variance > 0.05 {
		t.Fatalf("shifted tone varies (mean |Δ| = %v); it did not land on DC", variance)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sig := testSignal()
	now := time.Now()

	sess, err := newSession(audio.DefaultRegistry(), dir, sig, now, false)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	frame := testFrame(sig.FreqHz, 1024)
	aud, err := sess.Feed(frame, 0)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if len(aud) == 0 {
		t.Fatal("feed returned no audio")
	}
	rec, err := sess.finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(rec) != 1 || rec[0].FileFormat != "wav" {
		t.Fatalf("rows = %+v, want exactly the wav row", rec)
	}
	row := rec[0]
	if row.SignalID != sig.ID {
		t.Errorf("recording row = %+v", row)
	}
	if row.SampleRate != 48000 {
		t.Errorf("sample rate = %d, want 48000", row.SampleRate)
	}
	info, err := os.Stat(row.FilePath)
	if err != nil {
		t.Fatalf("recording file missing: %v", err)
	}
	if info.Size() <= 44 {
		t.Fatalf("file too small (%d bytes) for a fed session", info.Size())
	}
	raw, _ := os.ReadFile(row.FilePath)
	if !bytes.HasPrefix(raw, []byte("RIFF")) {
		t.Error("finalized file is not a WAV")
	}
}

func TestInBand(t *testing.T) {
	sig := testSignal() // 146.52 MHz
	frame := testFrame(146_000_000, 16)
	frame.SampleRate = 2_400_000
	if !inBand(sig, frame) {
		t.Error("146.52 MHz should be inside 146.0±1.2 MHz")
	}
	far := sig
	far.FreqHz = 915_000_000
	if inBand(far, frame) {
		t.Error("915 MHz must not be in band")
	}
	zero := testFrame(146_000_000, 16)
	zero.SampleRate = 0
	if inBand(sig, zero) {
		t.Error("zero sample rate must never match")
	}
}

func TestRecorderOpensClosesAndPersists(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecorder(Config{Dir: dir, CloseSilence: 50 * time.Millisecond}, nil)
	sig := testSignal()
	tracked := []db.Signal{sig}

	start := time.Now()
	rec.ObserveFrame(testFrame(146_500_000, 1024), tracked) // signal 20 kHz above center
	if got := rec.OpenSessions(); got != 1 {
		t.Fatalf("open sessions = %d, want 1", got)
	}
	// Fresh session: not closed before the hysteresis.
	if n := rec.CloseIdle(start.Add(10 * time.Millisecond)); n != 0 {
		t.Fatalf("closed %d sessions before hysteresis, want 0", n)
	}
	// Out-of-band frame must not open anything new.
	rec.ObserveFrame(testFrame(915_000_000, 16), tracked)
	if got := rec.OpenSessions(); got != 1 {
		t.Fatalf("out-of-band frame changed session count to %d", got)
	}

	finalized := rec.CloseIdle(start.Add(200 * time.Millisecond))
	if finalized != 1 {
		t.Fatalf("finalized = %d, want 1", finalized)
	}
	if got := rec.OpenSessions(); got != 0 {
		t.Fatalf("sessions left open = %d", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one WAV in %s (err=%v, n=%d)", dir, err, len(entries))
	}
	info, _ := entries[0].Info()
	// 1024 IQ pairs decimate to ~20 audio samples → 40 data bytes on a
	// 44-byte header: short but valid.
	if info.Size() <= 44 {
		t.Fatalf("recorded file carries no audio: %d bytes", info.Size())
	}
}

func TestRecorderConcurrentCap(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecorder(Config{Dir: dir, MaxConcurrent: 1}, nil)

	a := testSignal()
	b := testSignal()
	b.ID = "22222222-2222-2222-2222-222222222222"
	tracked := []db.Signal{a, b}

	rec.ObserveFrame(testFrame(146_520_000, 64), tracked)
	if got := rec.OpenSessions(); got != 1 {
		t.Fatalf("open sessions = %d, want 1 (cap enforced)", got)
	}
}

func TestSelectPurge(t *testing.T) {
	now := time.Now()
	mk := func(id string, age time.Duration, size int64) RecordingMeta {
		return RecordingMeta{ID: id, FilePath: id + ".wav", StartTime: now.Add(-age), SizeBytes: size}
	}
	const mib = 1 << 20
	recs := []RecordingMeta{
		mk("ancient", 40*24*time.Hour, 1<<10), // 1 KiB
		mk("old", 10*24*time.Hour, 40*mib),    // 40 MiB
		mk("new", 1*time.Hour, 1*mib),         // 1 MiB
	}

	// Age limit only.
	got := SelectPurge(recs, now, 30*24*time.Hour, 0)
	if len(got) != 1 || got[0].ID != "ancient" {
		t.Fatalf("age purge = %+v, want [ancient]", got)
	}

	// Size cap only: ~41 MiB total, budget 30 MiB → drop oldest until
	// the survivors fit: ancient (1 KiB) then old (40 MiB).
	got = SelectPurge(recs, now, 0, 30*mib)
	if len(got) != 2 || got[0].ID != "ancient" || got[1].ID != "old" {
		t.Fatalf("size purge = %+v, want [ancient old]", got)
	}

	// Combined: ancient goes by age; survivors are 41 MiB → old still
	// goes by size.
	got = SelectPurge(recs, now, 30*24*time.Hour, 30*mib)
	if len(got) != 2 || got[0].ID != "ancient" || got[1].ID != "old" {
		t.Fatalf("combined purge = %+v, want [ancient old]", got)
	}

	// No limits → nothing purged.
	if got := SelectPurge(recs, now, 0, 0); len(got) != 0 {
		t.Fatalf("no-limit purge = %+v, want empty", got)
	}
}

// TestSessionRawIQ (§10.5): with the raw-IQ side file enabled, a fed
// session finalizes into a wav row plus an iq row carrying the
// capture sample rate, and the .iq file is the headerless int16-LE
// interleave of the shifted baseband (offset 0 ⇒ the input samples).
func TestSessionRawIQ(t *testing.T) {
	dir := t.TempDir()
	sig := testSignal()

	sess, err := newSession(audio.DefaultRegistry(), dir, sig, time.Now(), true)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	frame := testFrame(sig.FreqHz, 512)
	if _, err := sess.Feed(frame, 0); err != nil {
		t.Fatalf("feed: %v", err)
	}
	rows, err := sess.finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	var wav, iq *db.Recording
	for i := range rows {
		switch rows[i].FileFormat {
		case "wav":
			wav = &rows[i]
		case "iq":
			iq = &rows[i]
		}
	}
	if wav == nil || iq == nil {
		t.Fatalf("rows = %+v, want one wav and one iq row", rows)
	}
	if iq.SampleRate != int32(frame.SampleRate) {
		t.Errorf("iq sample rate = %d, want capture rate %d", iq.SampleRate, frame.SampleRate)
	}
	if iq.CenterFreq != sig.FreqHz || iq.BandwidthHz != sig.BandwidthHz {
		t.Errorf("iq row = %+v", iq)
	}

	raw, err := os.ReadFile(iq.FilePath)
	if err != nil {
		t.Fatalf("read iq: %v", err)
	}
	// 512 complex samples → 1024 int16 → 2048 bytes, headerless.
	if len(raw) != len(frame.Samples)*2 {
		t.Fatalf("iq size = %d bytes, want %d", len(raw), len(frame.Samples)*2)
	}
	if iq.SizeBytes != int64(len(raw)) {
		t.Errorf("iq SizeBytes = %d, want %d", iq.SizeBytes, len(raw))
	}
	for i, want := range frame.Samples { // offset 0: the mixer is a no-op
		if got := int16(binary.LittleEndian.Uint16(raw[i*2:])); got != want {
			t.Fatalf("iq sample %d = %d, want %d", i, got, want)
		}
	}
	if _, err := os.Stat(wav.FilePath); err != nil {
		t.Fatalf("wav missing: %v", err)
	}
}

// TestRecorderIQOffByDefault (§10.5): without IQEnabled the session
// records the WAV only — no .iq file ever appears.
func TestRecorderIQOffByDefault(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecorder(Config{Dir: dir}, nil)
	sig := testSignal()
	start := time.Now()

	rec.ObserveFrame(testFrame(146_520_000, 256), []db.Signal{sig})
	if n := rec.CloseIdle(start.Add(15 * time.Second)); n != 1 {
		t.Fatalf("finalized = %d, want 1", n)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly one file (err=%v, n=%d)", err, len(entries))
	}
	if filepath.Ext(entries[0].Name()) != ".wav" {
		t.Fatalf("file = %s, want .wav only", entries[0].Name())
	}
}

// TestRecorderRawIQEnabled (§10.5): IQEnabled yields both files per
// finalized session.
func TestRecorderRawIQEnabled(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecorder(Config{Dir: dir, IQEnabled: true}, nil)
	sig := testSignal()
	start := time.Now()

	rec.ObserveFrame(testFrame(146_520_000, 256), []db.Signal{sig})
	if n := rec.CloseIdle(start.Add(15 * time.Second)); n != 1 {
		t.Fatalf("finalized = %d, want 1", n)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("want wav+iq files (err=%v, n=%d)", err, len(entries))
	}
	exts := map[string]bool{}
	for _, e := range entries {
		exts[filepath.Ext(e.Name())] = true
	}
	if !exts[".wav"] || !exts[".iq"] {
		t.Fatalf("extensions = %v, want .wav and .iq", exts)
	}
}

// TestRecorderMaxDurationCap (§11.1): CloseIdle finalizes a session
// once it runs past iq.max_duration_s even while audio keeps flowing,
// and the still-tracked signal rolls over into a fresh session.
func TestRecorderMaxDurationCap(t *testing.T) {
	rec := NewRecorder(Config{
		Dir:          t.TempDir(),
		CloseSilence: time.Hour, // silence must not be the trigger
		MaxDuration:  50 * time.Millisecond,
	}, nil)
	sig := testSignal()
	tracked := []db.Signal{sig}
	start := time.Now()

	rec.ObserveFrame(testFrame(146_520_000, 256), tracked)
	if n := rec.CloseIdle(start.Add(20 * time.Millisecond)); n != 0 {
		t.Fatalf("capped %d session(s) before max duration", n)
	}
	if n := rec.CloseIdle(start.Add(100 * time.Millisecond)); n != 1 {
		t.Fatalf("cap finalized %d session(s), want 1", n)
	}

	// Roll-over: the next in-band frame opens a fresh session.
	rec.ObserveFrame(testFrame(146_520_000, 256), tracked)
	if got := rec.OpenSessions(); got != 1 {
		t.Fatalf("open sessions after roll-over = %d, want 1", got)
	}
	rec.Close()
}

// TestRecorderCapDisabled (§11.1): a negative MaxDuration disables
// the cap entirely — only the silence hysteresis finalizes.
func TestRecorderCapDisabled(t *testing.T) {
	rec := NewRecorder(Config{
		Dir:          t.TempDir(),
		CloseSilence: 72 * time.Hour,
		MaxDuration:  -time.Second,
	}, nil)
	rec.ObserveFrame(testFrame(146_520_000, 64), []db.Signal{testSignal()})
	if n := rec.CloseIdle(time.Now().Add(24 * time.Hour)); n != 0 {
		t.Fatalf("cap fired despite being disabled: %d", n)
	}
	rec.Close()
}
