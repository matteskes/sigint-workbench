// Package api provides the REST API and WebSocket server.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog"

	"sigint-workbench/internal/db"
	"sigint-workbench/internal/ws"
)

// Server is the API gateway HTTP server.
type Server struct {
	router chi.Router
	db     *db.DB
	hub    *ws.Hub
	log    zerolog.Logger
}

// NewServer creates a new API server.
func NewServer(database *db.DB, hub *ws.Hub, log zerolog.Logger) *Server {
	s := &Server{
		db:  database,
		hub: hub,
		log: log,
	}
	s.buildRoutes()
	return s
}

func (s *Server) buildRoutes() {
	s.router = chi.NewRouter()

	s.router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// Health check
	s.router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Signals
	s.router.Get("/api/signals", s.handleGetSignals)
	s.router.Get("/api/signals/{id}", s.handleGetSignal)

	// Recordings
	s.router.Get("/api/recordings", s.handleGetRecordings)
	s.router.Get("/api/recordings/{id}/audio", s.handleGetRecordingAudio)

	// SDRs
	s.router.Get("/api/sdrs", s.handleGetSDRs)
	s.router.Put("/api/sdrs/{id}", s.handleUpdateSDR)

	// WebSocket
	s.router.Get("/ws", s.handleWebSocket)

	// Metrics (Prometheus)
	// s.router.Get("/metrics", promhttp.Handler().ServeHTTP)
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.router
}

// Start begins serving HTTP requests.
func (s *Server) Start(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}
	s.log.Info().Str("addr", addr).Msg("api-gateway: starting")
	return srv.ListenAndServe()
}

// handleGetSignals returns signals in the given bounding box.
func (s *Server) handleGetSignals(w http.ResponseWriter, r *http.Request) {
	// Parse bounding box from query params: ?minLat=&minLon=&maxLat=&maxLon=
	minLat := 90.0
	minLon := 180.0
	maxLat := -90.0
	maxLon := -180.0
	// TODO: parse query params with strconv

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	signals, err := s.db.GetSignals(ctx, minLat, minLon, maxLat, maxLon)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, signals)
}

// handleGetSignal returns a single signal by ID.
func (s *Server) handleGetSignal(w http.ResponseWriter, r *http.Request) {
	// TODO: implement
	w.Write([]byte(`{"error":"not implemented"}`))
}

// handleGetRecordings returns recordings, optionally filtered.
func (s *Server) handleGetRecordings(w http.ResponseWriter, r *http.Request) {
	// TODO: implement
	w.Write([]byte(`[]`))
}

// handleGetRecordingAudio serves a recorded audio file.
func (s *Server) handleGetRecordingAudio(w http.ResponseWriter, r *http.Request) {
	// TODO: implement — serve from RECORDINGS_DIR
	w.Write([]byte(`{"error":"not implemented"}`))
}

// handleGetSDRs returns all registered SDRs.
func (s *Server) handleGetSDRs(w http.ResponseWriter, r *http.Request) {
	// TODO: implement
	w.Write([]byte(`[]`))
}

// handleUpdateSDR updates an SDR's settings.
func (s *Server) handleUpdateSDR(w http.ResponseWriter, r *http.Request) {
	// TODO: implement
	w.Write([]byte(`{"error":"not implemented"}`))
}

// handleWebSocket upgrades to WebSocket for real-time events.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// TODO: implement WebSocket upgrade using s.hub
	w.Write([]byte(`{"error":"websocket not implemented"}`))
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	// TODO: use encoding/json
	_ = v
}