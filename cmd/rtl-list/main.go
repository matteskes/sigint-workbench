// Command rtl-list enumerates attached RTL-SDR dongles without opening
// them: one line per device with its USB index, product/tuner string
// and serial. Use it to pin the usb_index values in
// config/sdr-capture.yaml to physical dongles (docs/HARDWARE.md §2) —
// USB indices are enumeration order and can swap across a replug, so
// re-check after every hardware change.
//
// Build (real hardware — cgo, librtlsdr):
//
//	go build -tags rtlsdr -o bin/rtl-list ./cmd/rtl-list
//
// Built without the tag, DeviceCount reports 0 and this tool says so —
// the same convention as the stub drivers in internal/sdr.
package main

import (
	"fmt"
	"os"

	"sigint-workbench/internal/sdr"
)

func main() {
	n := sdr.DeviceCount()
	if n == 0 {
		fmt.Fprintln(os.Stderr, "rtl-list: no RTL-SDR devices found")
		fmt.Fprintln(os.Stderr,
			"(if librtlsdr is installed, rebuild with the driver:",
			"go build -tags rtlsdr ./cmd/rtl-list)")
		os.Exit(1)
	}

	fmt.Printf("%-5s %-28s %s\n", "INDEX", "MODEL", "SERIAL")
	for i := 0; i < n; i++ {
		dev, err := sdr.NewRTLSDR(fmt.Sprintf("rtl-%d", i), i)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rtl-list: index %d: %v\n", i, err)
			os.Exit(1)
		}
		meta := dev.Metadata()
		fmt.Printf("%-5d %-28s %s\n", i, meta.Model, meta.Serial)
	}
	fmt.Println()
	fmt.Println("Pin each dongle via usb_index in config/sdr-capture.yaml;")
	fmt.Println("indices are enumeration order and may change across a")
	fmt.Println("replug — match serials, not history (docs/HARDWARE.md §2).")
}
