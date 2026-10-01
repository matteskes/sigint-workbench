// signal-processor — FFT, peak detection, band identification.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sigint-workbench/internal/dsp"
)

func main() {
	configPath := flag.String("config", "config/signal-processor.yaml", "config path")
	flag.Parse()
	_ = configPath

	peakDetector := dsp.NewPeakDetector()
	_ = peakDetector

	fmt.Println("signal-processor: starting")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("signal-processor: stopped")
}