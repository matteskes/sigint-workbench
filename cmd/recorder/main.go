// recorder — Records raw IQ and decoded audio for detected signals.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sigint-workbench/internal/audio"
)

func main() {
	dbURL := os.Getenv("DB_URL")
	recordingsDir := flag.String("dir", "./recordings", "recordings directory")
	flag.Parse()
	_ = dbURL

	// Initialize demodulator registry
	registry := audio.DefaultRegistry()
	fmt.Printf("recorder: %d demodulators registered\n", len(registry.All()))

	// Ensure recordings directory exists
	if err := os.MkdirAll(*recordingsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "recorder: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("recorder: recordings dir: %s\n", *recordingsDir)
	fmt.Println("recorder: starting")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("recorder: stopped")
}