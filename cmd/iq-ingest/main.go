// iq-ingest — Receives IQ streams over UDP and dispatches to processors.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sigint-workbench/internal/sdr"
)

func main() {
	port := flag.Int("port", 9000, "UDP port to listen on")
	flag.Parse()

	receiver, frames, err := sdr.NewIQReceiver(*port, 256)
	if err != nil {
		fmt.Fprintf(os.Stderr, "iq-ingest: %v\n", err)
		os.Exit(1)
	}
	defer receiver.Close()

	fmt.Printf("iq-ingest: listening on UDP :%d\n", *port)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case frame := <-frames:
			// TODO: Dispatch to signal-processor, recorder
			_ = frame
		case sig := <-sigCh:
			fmt.Printf("\niq-ingest: shutting down (%v)\n", sig)
			return
		}
	}
}