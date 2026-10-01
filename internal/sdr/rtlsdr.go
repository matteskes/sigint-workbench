// Package sdr — RTL-SDR driver implementation.
//
// This file uses cgo to interface with librtlsdr.
// It is only compiled when the "rtlsdr" build tag is set.
//
// Build with:  go build -tags rtlsdr ./cmd/sdr-capture
// Requires:    librtlsdr-dev installed (brew install librtlsdr / apt install librtlsdr-dev)
//
//go:build rtlsdr

package sdr

/*
#cgo pkg-config: librtlsdr
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// RTLSDR implements the SDR interface for RTL2832U-based dongles.
type RTLSDR struct {
	device *C.int
	meta   SDRMetadata
	closed bool
}

// NewRTLSDR creates a new RTL-SDR device at the given USB index.
func NewRTLSDR(id string, usbIndex int) (*RTLSDR, error) {
	idx := C.int(usbIndex)

	var devCount C.int
	C.rtlsdr_get_device_count(&devCount)
	if idx >= devCount {
		return nil, fmt.Errorf("rtl-sdr: USB index %d out of range (found %d devices)", usbIndex, devCount)
	}

	var dev *C.uchar
	C.rtlsdr_get_device_usb_string(idx, (*C.uchar)(C.malloc(C.size_t(256))), 256)

	r := &RTLSDR{
		meta: SDRMetadata{
			ID:      id,
			Model:   "RTL2832U",
			FreqMin: 24_000_000,   // 24 MHz
			FreqMax: 1_700_000_000, // 1.7 GHz
			MaxBW:   3_200_000,   // 3.2 MHz max
			HasTX:   false,
		},
	}
	_ = dev
	_ = devCount
	return r, nil
}

// Open initializes the RTL-SDR device.
func (r *RTLSDR) Open() error {
	if r.closed {
		return fmt.Errorf("rtl-sdr: device already closed")
	}
	var dev C.int
	if C.rtlsdr_open(&dev, 0) < 0 {
		return fmt.Errorf("rtl-sdr: failed to open device")
	}
	r.device = &dev
	return nil
}

// Close releases the RTL-SDR device.
func (r *RTLSDR) Close() error {
	if !r.closed && r.device != nil {
		C.rtlsdr_close((*C.int)(r.device))
		r.closed = true
	}
	return nil
}

// SetFrequency tunes to the given center frequency in Hz.
func (r *RTLSDR) SetFrequency(hz uint64) error {
	if r.device == nil {
		return fmt.Errorf("rtl-sdr: device not open")
	}
	C.rtlsdr_set_center_freq((*C.int)(r.device), C.uint32_t(hz))
	return nil
}

// SetSampleRate sets the sample rate in Hz.
func (r *RTLSDR) SetSampleRate(hz uint32) error {
	if r.device == nil {
		return fmt.Errorf("rtl-sdr: device not open")
	}
	if hz > r.meta.MaxBW {
		hz = r.meta.MaxBW
	}
	C.rtlsdr_set_sample_rate((*C.int)(r.device), C.uint32_t(hz))
	return nil
}

// SetGain sets the RF gain in dB.
func (r *RTLSDR) SetGain(db float64) error {
	if r.device == nil {
		return fmt.Errorf("rtl-sdr: device not open")
	}
	C.rtlsdr_set_gain((*C.int)(r.device), C.float(db))
	return nil
}

// ReadIQ reads interleaved I/Q samples.
func (r *RTLSDR) ReadIQ(buf []int16) (int, error) {
	if r.device == nil {
		return 0, fmt.Errorf("rtl-sdr: device not open")
	}
	if len(buf) == 0 {
		return 0, nil
	}
	n := C.rtlsdr_read_sync(
		(*C.int)(r.device),
		unsafe.Pointer(&buf[0]),
		C.uint32_t(len(buf)),
		nil, // timeout
	)
	if n < 0 {
		return 0, fmt.Errorf("rtl-sdr: read error %d", int(n))
	}
	return int(n), nil
}

// Metadata returns the device metadata.
func (r *RTLSDR) Metadata() SDRMetadata {
	return r.meta
}