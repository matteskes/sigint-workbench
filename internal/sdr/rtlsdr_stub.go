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

func (r *RTLSDR) Open() error                     { return fmt.Errorf("rtl-sdr: built without rtlsdr support") }
func (r *RTLSDR) Close() error                    { return nil }
func (r *RTLSDR) SetFrequency(hz uint64) error    { return fmt.Errorf("rtl-sdr: unavailable") }
func (r *RTLSDR) SetSampleRate(hz uint32) error   { return fmt.Errorf("rtl-sdr: unavailable") }
func (r *RTLSDR) SetGain(db float64) error        { return fmt.Errorf("rtl-sdr: unavailable") }
func (r *RTLSDR) AppliedGainDB() (float64, bool)  { return 0, false }
func (r *RTLSDR) ReadIQ(buf []int16) (int, error) { return 0, fmt.Errorf("rtl-sdr: unavailable") }
func (r *RTLSDR) Metadata() SDRMetadata           { return SDRMetadata{ID: "rtlsdr", Model: "N/A"} }

// DeviceCount reports 0 when built without the rtlsdr tag.
func DeviceCount() int { return 0 }

// DeviceUSBStrings reports no hardware when built without the rtlsdr tag.
func DeviceUSBStrings(i int) (product, serial string, ok bool) { return "", "", false }
