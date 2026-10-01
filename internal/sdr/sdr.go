// Package sdr defines the hardware-agnostic SDR interface and drivers.
package sdr

import (
	"fmt"
	"sync"
)

// SDR is the interface that all SDR hardware drivers must implement.
// It is hardware-agnostic: the rest of the pipeline only sees this interface.
type SDR interface {
	// Open initializes the SDR device and prepares it for streaming.
	Open() error

	// Close releases the SDR device.
	Close() error

	// SetFrequency tunes the SDR to the given center frequency in Hz.
	SetFrequency(hz uint64) error

	// SetSampleRate sets the sample rate (bandwidth) in Hz.
	SetSampleRate(hz uint32) error

	// SetGain sets the RF gain in dB.
	SetGain(db float64) error

	// ReadIQ reads interleaved I/Q samples into buf.
	// buf must have length of even number of int16 values (I, Q pairs).
	// Returns the number of int16 values read.
	ReadIQ(buf []int16) (int, error)

	// Metadata returns information about this SDR device.
	Metadata() SDRMetadata
}

// SDRMetadata describes a physical SDR device.
type SDRMetadata struct {
	ID      string `json:"id" yaml:"id"`         // Logical ID, e.g. "rtlsdr-0"
	Model   string `json:"model" yaml:"model"`   // Hardware model, e.g. "RTL2832U"
	Serial  string `json:"serial,omitempty" yaml:"serial,omitempty"`
	FreqMin uint64 `json:"freqMin" yaml:"freqMin"` // Minimum frequency in Hz
	FreqMax uint64 `json:"freqMax" yaml:"freqMax"` // Maximum frequency in Hz
	MaxBW   uint32 `json:"maxBW" yaml:"maxBW"`     // Maximum bandwidth (sample rate) in Hz
	HasTX   bool   `json:"hasTX" yaml:"hasTX"`     // Whether the device supports transmit
}

// String returns a human-readable description of the SDR.
func (m SDRMetadata) String() string {
	tx := "RX"
	if m.HasTX {
		tx = "TX/RX"
	}
	return fmt.Sprintf("%s [%s] %s %d-%d MHz BW:%d MHz",
		m.ID, m.Model, tx,
		m.FreqMin/1_000_000, m.FreqMax/1_000_000,
		m.MaxBW/1_000_000)
}

// Registry manages a collection of SDR devices.
type Registry struct {
	mu    sync.RWMutex
	sdrs  map[string]SDR
	order []string // preserves insertion order
}

// NewRegistry creates an empty SDR registry.
func NewRegistry() *Registry {
	return &Registry{
		sdrs:  make(map[string]SDR),
		order: make([]string, 0),
	}
}

// Register adds an SDR to the registry.
func (r *Registry) Register(s SDR) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := s.Metadata().ID
	if _, exists := r.sdrs[id]; exists {
		return fmt.Errorf("sdr %q already registered", id)
	}
	r.sdrs[id] = s
	r.order = append(r.order, id)
	return nil
}

// Get returns an SDR by ID.
func (r *Registry) Get(id string) (SDR, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sdrs[id]
	return s, ok
}

// All returns all registered SDRs in insertion order.
func (r *Registry) All() []SDR {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]SDR, 0, len(r.order))
	for _, id := range r.order {
		result = append(result, r.sdrs[id])
	}
	return result
}

// MetadataList returns metadata for all registered SDRs.
func (r *Registry) MetadataList() []SDRMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]SDRMetadata, 0, len(r.order))
	for _, id := range r.order {
		result = append(result, r.sdrs[id].Metadata())
	}
	return result
}