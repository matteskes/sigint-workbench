// Package sdr — RTL-SDR stub for builds without the "rtlsdr" build tag.
//
// This allows the rest of the codebase to compile without librtlsdr installed.
//
//go:build !rtlsdr

package sdr

import "fmt"

// NewRTLSDR returns an error when built without the rtlsdr tag.
func NewRTLSDR(id string, usbIndex int) (*RTLSDR, error) {
	return nil, fmt.Errorf("rtl-sdr: built without rtlsdr support (use -tags rtlsdr)")
}

// RTLSDR is a placeholder when the rtlsdr build tag is not set.
type RTLSDR struct{}