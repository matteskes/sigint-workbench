// Package sdr — HackRF stub for builds without the "hackrf" build tag.
//
// Mirrors rtlsdr_stub.go: keeps the driver wiring compiling without
// libhackrf installed. The real driver lives in hackrf.go and is built
// with -tags hackrf. RX-only by policy (H2): even the stub offers no
// transmit path.
//
//go:build !hackrf

package sdr

import "fmt"

// HackRF is a placeholder when the hackrf build tag is not set.
type HackRF struct{}

// NewHackRF returns an error when built without the hackrf tag.
func NewHackRF(id string, serial string) (*HackRF, error) {
	return nil, fmt.Errorf("hackrf: built without hackrf support (use -tags hackrf; configured serial %q)", serial)
}

func (h *HackRF) Open() error                     { return fmt.Errorf("hackrf: built without hackrf support") }
func (h *HackRF) Close() error                    { return nil }
func (h *HackRF) SetFrequency(hz uint64) error    { return fmt.Errorf("hackrf: unavailable") }
func (h *HackRF) SetSampleRate(hz uint32) error   { return fmt.Errorf("hackrf: unavailable") }
func (h *HackRF) SetGain(db float64) error        { return fmt.Errorf("hackrf: unavailable") }
func (h *HackRF) ReadIQ(buf []int16) (int, error) { return 0, fmt.Errorf("hackrf: unavailable") }
func (h *HackRF) Metadata() SDRMetadata           { return SDRMetadata{ID: "hackrf", Model: "N/A"} }

// compile-time interface check.
var _ SDR = (*HackRF)(nil)
