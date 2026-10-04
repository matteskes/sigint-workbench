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

// SubtypeAware is implemented by demodulators that distinguish
// subtypes within a modulation family (WFM vs NFM, USB vs LSB).
// The registry prefers exact (modulation, subType) matches before
// falling back to modulation-only matching (§10.1).
type SubtypeAware interface {
	Demodulator
	CanHandlePair(modulation, subType string) bool
}

// Get returns a demodulator for the given (modulation, subType) pair.
// Selection is two-pass: an exact pair match via SubtypeAware wins;
// otherwise the first demodulator registered for the modulation
// family is returned (an empty or unrecognized subType always falls
// back to family order). The recorder MUST select on the full pair
// so a signal classified FM/NFM resolves to the 2.5 kHz demodulator,
// not the 25 kHz one (§10.1).
func (r *Registry) Get(modulation, subType string) (Demodulator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, d := range r.demodulators {
		if sd, ok := d.(SubtypeAware); ok {
			if sd.CanHandlePair(modulation, subType) {
				return d, nil
			}
		}
	}
	for _, d := range r.demodulators {
		if sd, ok := d.(SubtypeAware); ok {
			if sd.CanHandlePair(modulation, "") {
				return d, nil
			}
		} else if d.CanHandle(modulation) {
			return d, nil
		}
	}
	return nil, fmt.Errorf("audio: no demodulator for modulation %q (subType %q)", modulation, subType)
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
	// SSB upper (true sideband-select demodulation, §10.1)
	r.Register(NewSSBDemodulator(48000, "upper"))
	// SSB lower
	r.Register(NewSSBDemodulator(48000, "lower"))
	return r
}

// NormalizeModulation normalizes a modulation string for lookup.
func NormalizeModulation(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}