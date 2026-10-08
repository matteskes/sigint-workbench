package sdr

import (
	"testing"
)

func TestSimulator_OpenClose(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)

	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := sim.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestSimulator_ReadIQ_NotOpen(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)
	buf := make([]int16, 1024)
	_, err := sim.ReadIQ(buf)
	if err == nil {
		t.Error("expected error reading from closed simulator")
	}
}

func TestSimulator_ReadIQ_ProducesSamples(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)
	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sim.Close()

	buf := make([]int16, 4096)
	n, err := sim.ReadIQ(buf)
	if err != nil {
		t.Fatalf("ReadIQ failed: %v", err)
	}
	if n != len(buf) {
		t.Errorf("ReadIQ returned n=%d, expected %d", n, len(buf))
	}

	// Verify samples are not all zero (simulator produces signals)
	allZero := true
	for _, s := range buf {
		if s != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("all samples are zero — simulator should produce non-zero signal")
	}
}

func TestSimulator_MultipleReads(t *testing.T) {
	sim := NewSimulator(100_000_000, 1_000_000)
	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sim.Close()

	buf := make([]int16, 1024)
	for i := 0; i < 10; i++ {
		n, err := sim.ReadIQ(buf)
		if err != nil {
			t.Fatalf("ReadIQ call %d failed: %v", i, err)
		}
		if n != len(buf) {
			t.Fatalf("ReadIQ call %d: n=%d, expected %d", i, n, len(buf))
		}
	}
}

func TestSimulator_Metadata(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)
	meta := sim.Metadata()

	if meta.ID != "simulator-0" {
		t.Errorf("ID = %q, expected %q", meta.ID, "simulator-0")
	}
	if meta.Model != "Simulator" {
		t.Errorf("Model = %q, expected %q", meta.Model, "Simulator")
	}
	if meta.FreqMin != 24_000_000 {
		t.Errorf("FreqMin = %d, expected 24_000_000", meta.FreqMin)
	}
	if meta.FreqMax != 1_700_000_000 {
		t.Errorf("FreqMax = %d, expected 1_700_000_000", meta.FreqMax)
	}
}

func TestSimulator_SetSampleRate_Zero(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)
	err := sim.SetSampleRate(0)
	if err == nil {
		t.Error("expected error for zero sample rate")
	}
}

func TestSimulator_SetFrequency(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)
	if err := sim.SetFrequency(100_000_000); err != nil {
		t.Fatalf("SetFrequency failed: %v", err)
	}
}

func TestSimulator_NoiseLevel(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)
	sim.SetNoiseLevel(0.0)
	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sim.Close()

	buf := make([]int16, 2048)
	if _, err := sim.ReadIQ(buf); err != nil {
		t.Fatalf("ReadIQ failed: %v", err)
	}

	// With noise=0, output should still have signal (3 default signals)
	allZero := true
	for _, s := range buf {
		if s != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("with 3 signals and zero noise, output should not be all zeros")
	}
}

func TestSimulator_HardwareState_Default(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)

	// Before Open, state is zero value (Connected).
	if sim.HardwareState() != Connected {
		t.Errorf("pre-Open state = %v, want %v", sim.HardwareState(), Connected)
	}

	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sim.Close()

	// After Open, must be Connected.
	if sim.HardwareState() != Connected {
		t.Errorf("post-Open state = %v, want %v", sim.HardwareState(), Connected)
	}
}

func TestSimulator_SetHardwareState_Disconnected(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)
	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sim.Close()

	// Unplug the device.
	if err := sim.SetHardwareState(Disconnected); err != nil {
		t.Fatalf("SetHardwareState(Disconnected) failed: %v", err)
	}

	if sim.HardwareState() != Disconnected {
		t.Errorf("state after unplug = %v, want %v", sim.HardwareState(), Disconnected)
	}

	// ReadIQ MUST fail while disconnected.
	buf := make([]int16, 1024)
	if _, err := sim.ReadIQ(buf); err == nil {
		t.Error("ReadIQ while disconnected: expected error, got nil")
	} else {
		t.Logf("ReadIQ error (expected): %v", err)
	}
}

func TestSimulator_SetHardwareState_Reconnect(t *testing.T) {
	sim := NewSimulator(146_520_000, 2_000_000)
	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sim.Close()

	// Unplug, then reconnect.
	if err := sim.SetHardwareState(Disconnected); err != nil {
		t.Fatalf("SetHardwareState(Disconnected) failed: %v", err)
	}

	buf := make([]int16, 1024)
	if _, err := sim.ReadIQ(buf); err == nil {
		t.Error("ReadIQ while disconnected: expected error, got nil")
	}

	// Reconnect the device.
	if err := sim.SetHardwareState(Connected); err != nil {
		t.Fatalf("SetHardwareState(Connected) failed: %v", err)
	}

	if sim.HardwareState() != Connected {
		t.Errorf("state after reconnect = %v, want %v", sim.HardwareState(), Connected)
	}

	// ReadIQ must succeed after reconnect.
	if _, err := sim.ReadIQ(buf); err != nil {
		t.Fatalf("ReadIQ after reconnect: %v (backoff may still be in progress)", err)
	}
	t.Log("Reconnect successful: ReadIQ succeeds after SetHardwareState(Connected)")
}

func TestSimulator_HardwareState_String(t *testing.T) {
	tests := []struct {
		state DeviceState
		want  string
	}{
		{Connected, "connected"},
		{Disconnected, "disconnected"},
		{DeviceState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("DeviceState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}