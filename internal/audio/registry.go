// Package audio — demodulator registry.
package audio

import (
	"fmt"
	"strings"
	"sync"
)

// Registry manages available demodulators and selects the right one
// for a given modulation type.
type Registry struct {
	mu          sync.RWMutex
	demodulators []Demodulator
}

// NewRegistry creates an empty demodulator registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a demodulator to the registry.
func (r *Registry) Register(d Demodulator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.demodulators = append(r.demodulators, d)
}

// Get returns a demodulator that can handle the given modulation string.
func (r *Registry) Get(modulation string) (Demodulator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, d := range r.demodulators {
		if d.CanHandle(modulation) {
			return d, nil
		}
	}
	return nil, fmt.Errorf("audio: no demodulator for modulation %q", modulation)
}

// All returns all registered demodulators.
func (r *Registry) All() []Demodulator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Demodulator, len(r.demodulators))
	copy(result, r.demodulators)
	return result
}

// DefaultRegistry returns a registry pre-loaded with FM and AM demodulators.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	// Wide FM (aviation, land mobile, marine)
	r.Register(NewFMDemodulator(25000, 48000))
	// Narrow FM (ham, PMR)
	nfm := NewFMDemodulator(2500, 48000)
	r.Register(nfm)
	// Standard AM
	r.Register(NewAMDemodulator(48000, ""))
	// SSB upper
	r.Register(NewAMDemodulator(48000, "upper"))
	// SSB lower
	r.Register(NewAMDemodulator(48000, "lower"))
	return r
}

// NormalizeModulation normalizes a modulation string for lookup.
func NormalizeModulation(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}