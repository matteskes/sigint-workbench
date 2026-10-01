// Package sdr — HackRF driver (stub for future implementation).
//
// The HackRF One covers 1 MHz – 6 GHz and supports both TX and RX.
// This stub defines the interface; the full implementation will use
// the hackrf library via cgo (build tag: hackrf).
//
//go:build !hackrf

package sdr

import "fmt"

// HackRF is a placeholder for the HackRF One driver.
type HackRF struct{}

// NewHackRF creates a new HackRF device.
func NewHackRF(id string, serial string) (*HackRF, error) {
	return nil, fmt.Errorf("hackrf: not yet implemented (use -tags hackrf when available)")
}