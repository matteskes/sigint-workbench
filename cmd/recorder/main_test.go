package main

import "testing"

// §16.5 documents 48000; §10.2/§10.4 wire demod → AGC → Opus → WAV at
// 48 kHz. Unset falls back to the default; anything else refuses to
// start (A9) instead of recording pitch-shifted audio.
func TestResolveAudioSampleRate(t *testing.T) {
	hz, err := resolveAudioSampleRate(0)
	if err != nil || hz != 48000 {
		t.Fatalf("unset rate = (%d, %v), want (48000, nil)", hz, err)
	}
	hz, err = resolveAudioSampleRate(48000)
	if err != nil || hz != 48000 {
		t.Fatalf("48000 = (%d, %v), want (48000, nil)", hz, err)
	}
	for _, bad := range []uint32{44100, 96000, 16000} {
		if _, err := resolveAudioSampleRate(bad); err == nil {
			t.Fatalf("%d must be rejected: the audio stack is 48 kHz only", bad)
		}
	}
}
