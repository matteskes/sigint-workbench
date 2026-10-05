// Package sdr — RTL-SDR driver implementation.
//
// This file uses cgo to interface with librtlsdr.
// It is only compiled when the "rtlsdr" build tag is set.
//
// Build with:  go build -tags rtlsdr ./cmd/sdr-capture
// Requires:    librtlsdr-dev installed (brew install librtlsdr / apt install librtlsdr-dev)
//
// Binding notes (verified against rtl-sdr.h):
//   - rtlsdr_open takes rtlsdr_dev_t ** and must be given the
//     configured USB index (not hardcoded 0).
//   - rtlsdr_read_sync counts BYTES. The RTL2832U delivers one
//     unsigned 8-bit I or Q sample per byte, so a []int16 buffer
//     holds one converted sample per slot: request len(buf) bytes
//     and scale each byte to full-scale int16 via (b-128)<<8, the
//     convention the pipeline assumes (float64(v)/32768.0).
//   - Tuner gain is set in tenths of dB after switching the tuner
//     to manual gain mode.
//
//go:build rtlsdr

package sdr

/*
#cgo pkg-config: librtlsdr
#include <stdlib.h>
#include <rtl-sdr.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// RTLSDR implements the SDR interface for RTL2832U-based dongles.
type RTLSDR struct {
	mu        sync.Mutex
	dev       *C.rtlsdr_dev_t
	index     uint32
	meta      SDRMetadata
	closed    bool
	readBuf   []byte    // reusable u8 scratch for ReadIQ (guarded by mu)
	gainTable []float64 // supported tuner gains in dB, queried once (guarded by mu)
	gainDB    float64   // last applied manual gain (guarded by mu)
	gainSet   bool      // manual gain applied — false while auto/unset (guarded by mu)
}

// NewRTLSDR creates a new RTL-SDR device at the given USB index.
// The index is honored by Open (§15.3 defect 2); the device name,
// product and serial strings are queried for metadata without
// opening the device.
func NewRTLSDR(id string, usbIndex int) (*RTLSDR, error) {
	if usbIndex < 0 {
		return nil, fmt.Errorf("rtl-sdr: invalid USB index %d", usbIndex)
	}
	idx := C.uint32_t(usbIndex)

	devCount := int(C.rtlsdr_get_device_count())
	if usbIndex >= devCount {
		return nil, fmt.Errorf("rtl-sdr: USB index %d out of range (found %d devices)", usbIndex, devCount)
	}

	model := "RTL2832U"
	if name := C.rtlsdr_get_device_name(idx); name != nil {
		if s := C.GoString(name); s != "" {
			model = s
		}
	}

	serial := ""
	var manufact [256]C.char
	var product [256]C.char
	var serialBuf [256]C.char
	if C.rtlsdr_get_device_usb_strings(idx,
		(*C.char)(unsafe.Pointer(&manufact[0])),
		(*C.char)(unsafe.Pointer(&product[0])),
		(*C.char)(unsafe.Pointer(&serialBuf[0]))) == 0 {
		if p := C.GoString((*C.char)(unsafe.Pointer(&product[0]))); p != "" {
			model = p
		}
		serial = C.GoString((*C.char)(unsafe.Pointer(&serialBuf[0])))
	}

	return &RTLSDR{
		index: uint32(idx),
		meta: SDRMetadata{
			ID:      id,
			Model:   model,
			Serial:  serial,
			FreqMin: 24_000_000,    // 24 MHz
			FreqMax: 1_700_000_000, // 1.7 GHz
			MaxBW:   3_200_000,     // 3.2 MHz max
			HasTX:   false,
		},
	}, nil
}

// Open initializes the RTL-SDR device at the configured USB index
// and resets the demod buffer so stale samples from a previous
// session are not returned by ReadIQ.
func (r *RTLSDR) Open() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("rtl-sdr: device already closed")
	}
	if r.dev != nil {
		return fmt.Errorf("rtl-sdr: device already open")
	}
	var dev *C.rtlsdr_dev_t
	if C.rtlsdr_open(&dev, C.uint32_t(r.index)) < 0 {
		return fmt.Errorf("rtl-sdr: failed to open device at USB index %d", r.index)
	}
	if C.rtlsdr_reset_buffer(dev) < 0 {
		C.rtlsdr_close(dev)
		return fmt.Errorf("rtl-sdr: failed to reset buffer")
	}
	r.dev = dev
	return nil
}

// Close releases the RTL-SDR device.
func (r *RTLSDR) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dev != nil {
		C.rtlsdr_close(r.dev)
		r.dev = nil
	}
	r.closed = true
	return nil
}

// SetFrequency tunes to the given center frequency in Hz.
func (r *RTLSDR) SetFrequency(hz uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dev == nil {
		return fmt.Errorf("rtl-sdr: device not open")
	}
	if C.rtlsdr_set_center_freq(r.dev, C.uint32_t(hz)) < 0 {
		return fmt.Errorf("rtl-sdr: set_center_freq(%d Hz) failed", hz)
	}
	return nil
}

// SetSampleRate sets the sample rate in Hz, clamped to the device
// maximum.
func (r *RTLSDR) SetSampleRate(hz uint32) error {
	if hz == 0 {
		return fmt.Errorf("rtl-sdr: sample rate must be > 0")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dev == nil {
		return fmt.Errorf("rtl-sdr: device not open")
	}
	if hz > r.meta.MaxBW {
		hz = r.meta.MaxBW
	}
	if C.rtlsdr_set_sample_rate(r.dev, C.uint32_t(hz)) < 0 {
		return fmt.Errorf("rtl-sdr: set_sample_rate(%d Hz) failed", hz)
	}
	return nil
}

// SetGain sets the RF gain in dB. The tuner is switched to manual
// gain mode (librtlsdr takes gain in tenths of dB); a negative value
// enables automatic gain instead. Manual gains are validated against
// the tuner's supported gain table: requests within 3 dB of a
// supported step snap to that step and the applied figure is what
// AppliedGainDB reports; anything further is rejected — the r82xx
// silently clamps out-of-table values to its maximum, which would
// leave the hardware at half the configured gain while §7.4/§5.6
// report the configured figure (§15.3 defect 4).
func (r *RTLSDR) SetGain(db float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dev == nil {
		return fmt.Errorf("rtl-sdr: device not open")
	}
	if db < 0 {
		if C.rtlsdr_set_tuner_gain_mode(r.dev, 0) < 0 {
			return fmt.Errorf("rtl-sdr: set_tuner_gain_mode(auto) failed")
		}
		r.gainSet = false
		return nil
	}
	if C.rtlsdr_set_tuner_gain_mode(r.dev, 1) < 0 {
		return fmt.Errorf("rtl-sdr: set_tuner_gain_mode(manual) failed")
	}
	applied, err := r.applyGainLocked(db)
	if err != nil {
		return err
	}
	r.gainDB = applied
	r.gainSet = true
	return nil
}

// applyGainLocked validates db against the tuner's supported gain
// table (queried from the device once and cached) and applies the
// nearest supported step. Returns the applied gain.
func (r *RTLSDR) applyGainLocked(db float64) (float64, error) {
	if r.gainTable == nil {
		r.gainTable = []float64{}
		if n := C.rtlsdr_get_tuner_gains(r.dev, nil); n > 0 {
			buf := make([]C.int, n)
			if C.rtlsdr_get_tuner_gains(r.dev, &buf[0]) == n {
				for _, g := range buf {
					r.gainTable = append(r.gainTable, float64(g)/10)
				}
			}
		}
	}
	applied, ok := nearestGain(r.gainTable, db)
	if !ok {
		return 0, fmt.Errorf(
			"rtl-sdr: tuner gain %.1f dB not supported (nearest supported step %.1f dB); "+
				"pick a supported step — the tuner would silently run at %.1f dB "+
				"while the config and §5.6 dBm claim %.1f dB",
			db, applied, applied, db)
	}
	if C.rtlsdr_set_tuner_gain(r.dev, C.int(applied*10)) < 0 {
		return 0, fmt.Errorf("rtl-sdr: set_tuner_gain(%.1f dB) failed", applied)
	}
	return applied, nil
}

// AppliedGainDB reports the manual gain currently applied to the
// tuner — the snapped-to-step figure the hardware actually runs at,
// not the requested one. ok is false while auto gain is active or no
// manual gain has been applied yet (§5.6 calibration is invalid with
// auto gain anyway). Part of the optional interface sdr-capture uses
// to keep the §7.4 status and §5.6 applied_gain_db honest.
func (r *RTLSDR) AppliedGainDB() (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dev == nil || !r.gainSet {
		return 0, false
	}
	return r.gainDB, true
}

// ReadIQ reads interleaved I/Q samples. The dongle delivers one
// unsigned 8-bit sample per byte; each byte is scaled to a
// full-scale int16 ((b-128)<<8) so downstream consumers can keep
// dividing by 32768. The returned count is an even int16 count
// (complete I/Q pairs), per the SDR interface contract.
func (r *RTLSDR) ReadIQ(buf []int16) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dev == nil {
		return 0, fmt.Errorf("rtl-sdr: device not open")
	}
	if len(buf) == 0 {
		return 0, nil
	}

	// One u8 sample per int16 slot: request len(buf) bytes (the
	// byte/element fix, §15.3 defect 1 — NOT len(buf)*2, which would
	// overshoot MaxIQSamplesPerFrame and mis-pair I/Q).
	if len(r.readBuf) < len(buf) {
		r.readBuf = make([]byte, len(buf))
	}
	raw := r.readBuf[:len(buf)]

	var nRead C.int
	if C.rtlsdr_read_sync(r.dev, unsafe.Pointer(&raw[0]), C.int(len(raw)), &nRead) < 0 {
		return 0, fmt.Errorf("rtl-sdr: read_sync failed")
	}

	n := int(nRead)
	if n < 0 {
		n = 0
	}
	if n > len(raw) {
		n = len(raw)
	}
	if n%2 != 0 {
		n-- // keep complete I/Q pairs only
	}
	for i := 0; i < n; i++ {
		buf[i] = (int16(raw[i]) - 128) << 8
	}
	return n, nil
}

// Metadata returns the device metadata.
func (r *RTLSDR) Metadata() SDRMetadata {
	return r.meta
}

// DeviceCount returns the number of RTL-SDR devices attached to the
// host without opening any of them (librtlsdr enumerates by USB
// index). Used by cmd/rtl-list; built only with the rtlsdr tag — the
// stub returns 0.
func DeviceCount() int {
	return int(C.rtlsdr_get_device_count())
}

// DeviceUSBStrings returns the product name and serial of the RTL-SDR
// at USB index i without opening it (sdr-capture -devices). ok is
// false when the index is out of range or the strings are unreadable.
func DeviceUSBStrings(i int) (product, serial string, ok bool) {
	if i < 0 || i >= DeviceCount() {
		return "", "", false
	}
	var manufact, productBuf, serialBuf [256]C.char
	if C.rtlsdr_get_device_usb_strings(C.uint32_t(i),
		(*C.char)(unsafe.Pointer(&manufact[0])),
		(*C.char)(unsafe.Pointer(&productBuf[0])),
		(*C.char)(unsafe.Pointer(&serialBuf[0]))) != 0 {
		return "", "", false
	}
	return C.GoString((*C.char)(unsafe.Pointer(&productBuf[0]))),
		C.GoString((*C.char)(unsafe.Pointer(&serialBuf[0]))),
		true
}
