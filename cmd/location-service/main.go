// location-service — Signal location, tracking, and verification.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sigint-workbench/internal/location"
)

func main() {
	_ = flag.String("config", "config/location-service.yaml", "config path")
	flag.Parse()

	verifier := location.NewVerifier()
	_ = verifier

	fmt.Println("location-service: starting")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("location-service: stopped")
}