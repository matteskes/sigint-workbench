// api-gateway — REST API and audio file serving.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"

	"sigint-workbench/internal/api"
	"sigint-workbench/internal/db"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port")
	flag.Parse()

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		dbURL = "postgres://sdr:sdr@localhost:5432/sdr?sslmode=disable"
	}

	log := zerolog.New(os.Stderr).With().Timestamp().Logger()

	// Connect to database
	ctx := context.Background()
	database, err := db.New(ctx, dbURL)
	if err != nil {
		log.Warn().Err(err).Msg("database not available, continuing without")
		database = nil
	}
	if database != nil {
		defer database.Close()
	}

	// Create and start API server
	server := api.NewServer(database, log)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx, fmt.Sprintf(":%d", *port))
	}()

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatal().Err(err).Msg("api-gateway: server error")
		}
	case sig := <-sigCh:
		log.Info().Msgf("api-gateway: shutting down (%v)", sig)
	}
}