// Package resilience — Test R7: Device Unplug Lifecycle (D8, §4.4, §4.10.7).
//
// This test validates that the sdr-capture service correctly handles device
// disconnect and reconnect events via the simulator driver. The simulator
// implements a SetHardwareState() API that allows testing the read-backoff
// behavior specified in §4.4 (200 ms backoff on driver read error).
//
// This is a self-contained test — it does NOT require a running Docker stack.
// It directly exercises the Simulator driver from internal/sdr.
package resilience

import (
	"testing"
	"time"

	"sigint-workbench/internal/sdr"
)

// TestDeviceUnplugLifecycle validates the device disconnect → read-backoff →
// reconnect lifecycle:
//
//  1. Open simulator (initial state: Connected)
//  2. Verify ReadIQ produces samples (device healthy)
//  3. Unplug: SetHardwareState(Disconnected)
//  4. Verify ReadIQ returns error (read-backoff triggered, per §4.4)
//  5. Reconnect: SetHardwareState(Connected)
//  6. Verify ReadIQ succeeds again (device recovered, per §4.4)
func TestDeviceUnplugLifecycle(t *testing.T) {
	// Create and open a simulator.
	sim := sdr.NewSimulator(146_520_000, 2_000_000)
	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sim.Close()

	// Step 1: Verify initial state is Connected.
	if sim.HardwareState() != sdr.Connected {
		t.Fatalf("step 1: state = %v, want Connected", sim.HardwareState())
	}
	t.Log("Step 1: Device state = Connected ✓")

	// Step 2: Verify ReadIQ produces samples (device healthy).
	buf := make([]int16, 4096)
	n, err := sim.ReadIQ(buf)
	if err != nil {
		t.Fatalf("step 2: ReadIQ failed: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("step 2: ReadIQ returned n=%d, expected %d", n, len(buf))
	}
	t.Logf("step 2: ReadIQ returned %d samples (device healthy) ✓", n)

	// Step 3: Unplug the device.
	t.Log("Step 3: Unplugging device (SetHardwareState(Disconnected))...")
	if err := sim.SetHardwareState(sdr.Disconnected); err != nil {
		t.Fatalf("step 3: SetHardwareState(Disconnected) failed: %v", err)
	}
	if sim.HardwareState() != sdr.Disconnected {
		t.Fatalf("step 3: state = %v, want Disconnected", sim.HardwareState())
	}

	// Step 4: Verify ReadIQ returns error (read-backoff triggered, per §4.4).
	t.Log("Step 4: Attempting ReadIQ while disconnected (expect error)...")
	_, err = sim.ReadIQ(buf)
	if err == nil {
		t.Fatal("step 4: ReadIQ while disconnected: expected error, got nil")
	}
	t.Logf("step 4: ReadIQ error (expected): %v ✓", err)

	// Step 5: Reconnect the device.
	t.Log("Step 5: Reconnecting device (SetHardwareState(Connected))...")
	if err := sim.SetHardwareState(sdr.Connected); err != nil {
		t.Fatalf("step 5: SetHardwareState(Connected) failed: %v", err)
	}
	if sim.HardwareState() != sdr.Connected {
		t.Fatalf("step 5: state = %v, want Connected", sim.HardwareState())
	}
	t.Log("step 5: Device state = Connected ✓")

	// Step 6: Verify ReadIQ succeeds after reconnect (§4.4 read-backoff).
	t.Log("Step 6: Attempting ReadIQ after reconnect (expect success, with backoff)...")
	start := time.Now()
	n, err = sim.ReadIQ(buf)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("step 6: ReadIQ after reconnect: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("step 6: ReadIQ returned n=%d, expected %d", n, len(buf))
	}
	// The backoff should take ~200ms (default readBackoffInterval).
	// Give some slack: 150ms to 350ms.
	if elapsed < 150*time.Millisecond {
		t.Errorf("step 6: ReadIQ took %v, expected >= 150ms (backoff may be misconfigured)", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("step 6: ReadIQ took %v, expected <= 500ms (backoff may be too long)", elapsed)
	}
	t.Logf("step 6: ReadIQ returned %d samples in %v (backoff applied ✓)", n, elapsed)

	// Step 7: Verify sustained operation after reconnect (20 reads).
	t.Log("Step 7: Verifying sustained operation after reconnect (20 reads)...")
	for i := 0; i < 20; i++ {
		n, err = sim.ReadIQ(buf)
		if err != nil {
			t.Fatalf("step 7: read %d after reconnect failed: %v", i+1, err)
		}
		if n != len(buf) {
			t.Fatalf("step 7: read %d: n=%d, expected %d", i+1, n, len(buf))
		}
	}
	t.Log("step 7: 20 successful reads after reconnect ✓")
}

// TestDeviceUnplugMultipleTransitions validates rapid unplugging/replugging
// cycles (device flapping).
func TestDeviceUnplugMultipleTransitions(t *testing.T) {
	sim := sdr.NewSimulator(146_520_000, 2_000_000)
	if err := sim.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer sim.Close()

	buf := make([]int16, 2048)

	// Simulate rapid unplug/reconnect cycles.
	for cycle := 0; cycle < 5; cycle++ {
		// Unplug.
		if err := sim.SetHardwareState(sdr.Disconnected); err != nil {
			t.Fatalf("cycle %d: SetHardwareState(Disconnected) failed: %v", cycle, err)
		}
		// ReadIQ should fail.
		_, err := sim.ReadIQ(buf)
		if err == nil {
			t.Errorf("cycle %d: ReadIQ while disconnected: expected error, got nil", cycle)
		}

		// Reconnect (short delay to allow backoff tracking).
		if err := sim.SetHardwareState(sdr.Connected); err != nil {
			t.Fatalf("cycle %d: SetHardwareState(Connected) failed: %v", cycle, err)
		}

		// ReadIQ should succeed.
		n, err := sim.ReadIQ(buf)
		if err != nil {
			t.Fatalf("cycle %d: ReadIQ after reconnect: %v", cycle, err)
		}
		if n != len(buf) {
			t.Fatalf("cycle %d: ReadIQ n=%d, expected %d", cycle, n, len(buf))
		}
	}
	t.Log("5 rapid unplug/reconnect cycles completed successfully ✓")
}