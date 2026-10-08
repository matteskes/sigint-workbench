// Package sdr — synthetic IQ data simulator for pipeline testing.
//
// The simulator generates CW, FM, and AM signals at configurable
// offset frequencies plus white noise. It implements the SDR
// interface so the rest of the pipeline is hardware-agnostic.
//
// Usage:
//
//	sim := sdr.NewSimulator(146_520_000, 2_000_000)
//	sim.Open()
//	buf := make([]int16, 8192)
//	n, _ := sim.ReadIQ(buf)
package sdr

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// SimSignal describes one synthetic signal in the simulation.
type SimSignal struct {
	OffsetHz  float64 // Hz offset from center frequency (positive = above)
	Amplitude float64 // 0.0 to 1.0
	FMDevHz   float64 // FM frequency deviation in Hz (0 = no FM)
	ModHz     float64 // Modulating frequency in Hz (0 = CW or pure tone)
}

// Simulator generates synthetic IQ data that implements the SDR interface.
// It is safe for concurrent use (ReadIQ, SetFrequency, etc.).
type Simulator struct {
	mu         sync.Mutex
	centerFreq uint64
	sampleRate uint32
	gain       float64
	running    bool
	noiseLevel float64
	rng        *rand.Rand
	meta       SDRMetadata

	// Signal generation state
	signals []SimSignal
	phases  []float64 // per-signal phase accumulator (radians)
	t       float64   // time accumulator (seconds)
	// Device state (R7: Device Unplug Lifecycle)
	hardwareState DeviceState
	// reconnecting tracks the last setConnectedTime for backoff calculation
	setConnectedTime time.Time
}

// NewSimulator creates a simulator centred on centerFreq Hz with the
// given sample rate. Three default test signals are added:
//
//   - CW tone at +100 kHz
//   - FM (5 kHz dev, 1 kHz mod) at +500 kHz
//   - AM (800 Hz mod) at -200 kHz
func NewSimulator(centerFreq uint64, sampleRate uint32) *Simulator {
	s := &Simulator{
		centerFreq: centerFreq,
		sampleRate: sampleRate,
		gain:       40,
		noiseLevel: 0.005,
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		meta: SDRMetadata{
			ID:      "simulator-0",
			Model:   "Simulator",
			FreqMin: 24_000_000,
			FreqMax: 1_700_000_000,
			MaxBW:   10_000_000,
		},
	}
	s.AddSignal(SimSignal{OffsetHz: 100_000, Amplitude: 0.5})
	s.AddSignal(SimSignal{OffsetHz: 500_000, Amplitude: 0.4, FMDevHz: 5000, ModHz: 1000})
	s.AddSignal(SimSignal{OffsetHz: -200_000, Amplitude: 0.3, ModHz: 800})
	return s
}

// ── SDR interface ────────────────────────────────────────────────────

func (s *Simulator) Open() error {
	s.mu.Lock()
	s.running = true
	s.t = 0
	s.hardwareState = Connected
	s.setConnectedTime = time.Now()
	if len(s.phases) < len(s.signals) {
		s.phases = make([]float64, len(s.signals))
	}
	s.mu.Unlock()
	return nil
}

func (s *Simulator) Close() error {
	s.mu.Lock()
	s.running = false
	s.hardwareState = Disconnected
	s.mu.Unlock()
	return nil
}

func (s *Simulator) SetFrequency(hz uint64) error {
	s.mu.Lock()
	s.centerFreq = hz
	s.mu.Unlock()
	return nil
}

func (s *Simulator) SetSampleRate(hz uint32) error {
	if hz == 0 {
		return fmt.Errorf("simulator: sample rate must be > 0")
	}
	s.mu.Lock()
	s.sampleRate = hz
	s.mu.Unlock()
	return nil
}

func (s *Simulator) SetGain(db float64) error {
	s.mu.Lock()
	s.gain = db
	s.mu.Unlock()
	return nil
}

func (s *Simulator) Metadata() SDRMetadata { return s.meta }

// ── Configuration helpers ────────────────────────────────────────────

// AddSignal adds a synthetic signal to the simulation.
func (s *Simulator) AddSignal(sig SimSignal) {
	s.mu.Lock()
	s.signals = append(s.signals, sig)
	for len(s.phases) < len(s.signals) {
		s.phases = append(s.phases, 0)
	}
	s.mu.Unlock()
}

// SetNoiseLevel sets the white-noise amplitude (0.0 to 1.0).
func (s *Simulator) SetNoiseLevel(level float64) {
	s.mu.Lock()
	s.noiseLevel = level
	s.mu.Unlock()
}

// ── IQ generation ────────────────────────────────────────────────────

// ReadIQ fills buf with synthetic interleaved I/Q int16 samples.
// buf length must be even (I, Q pairs). Returns the number of int16
// values written (== len(buf) on success).
//
// When the device is in the Disconnected state, ReadIQ returns an error.
// When the device has just been reconnected (SetHardwareState(Connected)),
// ReadIQ applies the §4.4 read-backoff delay before producing samples,
// mimicking hardware ramp-up time.
func (s *Simulator) ReadIQ(buf []int16) (int, error) {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return 0, fmt.Errorf("simulator: not open")
	}
	if s.hardwareState == Disconnected {
		s.mu.Unlock()
		return 0, fmt.Errorf("simulator: device disconnected")
	}
	sr := float64(s.sampleRate)
	n := len(buf) / 2
	if n == 0 {
		s.mu.Unlock()
		return 0, nil
	}

	// §4.4 read-backoff: after reconnecting, sleep for the backoff
	// interval to mimic hardware ramp-up time.
	now := time.Now()
	elapsed := now.Sub(s.setConnectedTime)
	if elapsed < readBackoffInterval {
		remaining := readBackoffInterval - elapsed
		s.mu.Unlock()
		time.Sleep(remaining)
		s.mu.Lock()
	}
	// Snapshot signal config and phase state (avoid holding lock in loop)
	sigCount := len(s.signals)
	offsets := make([]float64, sigCount)
	amplitudes := make([]float64, sigCount)
	fmDevs := make([]float64, sigCount)
	modHzs := make([]float64, sigCount)
	for i := range s.signals {
		offsets[i] = s.signals[i].OffsetHz
		amplitudes[i] = s.signals[i].Amplitude
		fmDevs[i] = s.signals[i].FMDevHz
		modHzs[i] = s.signals[i].ModHz
	}
	phases := make([]float64, sigCount)
	copy(phases, s.phases)
	t := s.t
	noise := s.noiseLevel
	gainFactor := math.Pow(10, s.gain/40.0)
	s.mu.Unlock()

	dt := 1.0 / sr
	twoPi := 2 * math.Pi

	for i := 0; i < n; i++ {
		t += dt

		// Noise
		re := noise * (s.rng.Float64()*2 - 1)
		im := noise * (s.rng.Float64()*2 - 1)

		// Sum all signals
		for j := 0; j < sigCount; j++ {
			if fmDevs[j] > 0 && modHzs[j] > 0 {
				// FM: instantaneous frequency modulation
				instFreq := offsets[j] + fmDevs[j]*math.Sin(twoPi*modHzs[j]*t)
				phases[j] += twoPi * instFreq * dt
			} else {
				// CW or AM carrier
				phases[j] += twoPi * offsets[j] * dt
			}

			amp := amplitudes[j]
			// AM: amplitude modulation (only when FM is off)
			if modHzs[j] > 0 && fmDevs[j] == 0 {
				amp *= 1.0 + 0.5*math.Sin(twoPi*modHzs[j]*t)
			}

			re += amp * math.Cos(phases[j])
			im += amp * math.Sin(phases[j])
		}

		// Apply gain and clamp to int16
		re *= gainFactor
		im *= gainFactor
		buf[i*2] = int16(simClamp(re))
		buf[i*2+1] = int16(simClamp(im))
	}

	// Persist state
	s.mu.Lock()
	s.t = t
	copy(s.phases, phases)
	s.mu.Unlock()

	return len(buf), nil
}

func simClamp(v float64) float64 {
	if v < -32767 {
		return -32767
	}
	if v > 32767 {
		return 32767
	}
	return v
}

// ── Device state (R7: Device Unplug Lifecycle) ────────────────────────────

// DeviceState represents the current physical connection state of an SDR device.
type DeviceState int

const (
	// Connected means the device is physically connected and ready to stream.
	Connected DeviceState = iota
	// Disconnected means the device has been unplugged or is otherwise
	// unavailable. ReadIQ MUST return an error while in this state.
	Disconnected
)

// String returns a human-readable device state name.
func (s DeviceState) String() string {
	switch s {
	case Connected:
		return "connected"
	case Disconnected:
		return "disconnected"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// SetHardwareState sets the device's physical connection state.
// It is a simulator-only method used by the R7 (Device Unplug Lifecycle)
// e2e test to simulate unplugging and replugging a device (§4.4, §4.10.7).
//
// When the state transitions from Disconnected to Connected, the simulator
// implements the §4.4 read-backoff contract: subsequent ReadIQ calls will
// sleep for readBackoffInterval (default 200 ms) before producing samples,
// mimicking the hardware ramp-up time after a device reconnect.
func (s *Simulator) SetHardwareState(state DeviceState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hardwareState = state
	if state == Connected {
		s.setConnectedTime = time.Now()
	}
	return nil
}

// HardwareState returns the current physical connection state of the device.
func (s *Simulator) HardwareState() DeviceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hardwareState
}

// readBackoffInterval is the §4.4 read-backoff delay after reconnecting.
// Exposed as a package-level variable so tests can override it.
var readBackoffInterval = 200 * time.Millisecond