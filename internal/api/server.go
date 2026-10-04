// Package api provides the REST API and WebSocket server.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"sigint-workbench/internal/db"
	"sigint-workbench/internal/ws"
)

// Server is the API gateway HTTP server.
type Server struct {
	router chi.Router
	db     *db.DB
	log    zerolog.Logger

	recordingsDir string
	wsHubAddr     string // ws-hub host:port for the /ws relay (§2.2)
}

// NewServer creates a new API server.
func NewServer(database *db.DB, log zerolog.Logger) *Server {
	dir := os.Getenv("RECORDINGS_DIR")
	if dir == "" {
		dir = "recordings"
	}
	hubAddr := os.Getenv("WS_HUB_ADDR")
	if hubAddr == "" {
		hubAddr = "127.0.0.1:8081"
	}
	s := &Server{
		db:            database,
		log:           log,
		recordingsDir: dir,
		wsHubAddr:     hubAddr,
	}
	s.buildRoutes()
	return s
}

func (s *Server) buildRoutes() {
	s.router = chi.NewRouter()

	s.router.Use(cors.Handler(cors.Options{
		// AllowOriginFunc (not AllowedOrigins) is deliberate: in
		// go-chi/cors v1.2.1 an empty AllowedOrigins list silently
		// degrades to allow-all, and the func form also gives us the
		// deny-all behavior for set-but-empty ALLOWED_ORIGINS.
		AllowOriginFunc: func(r *http.Request, origin string) bool {
			return ws.CheckOrigin(ws.AllowedOriginsFromEnv(), r, origin)
		},
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

	// WebSocket event relay (§2.2, A3): the gateway is the single
	// client ingress; the former in-process hub had no producers and
	// was removed (§13.2.4).
	s.router.Get("/ws", s.handleWSRelay)

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

// requireDB responds 503 when the database is unavailable.
func (s *Server) requireDB(w http.ResponseWriter) bool {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// handleGetSignals returns signals in the given bounding box.
// Query params: ?minLat=&minLon=&maxLat=&maxLon= (default: whole world).
func (s *Server) handleGetSignals(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	q := r.URL.Query()
	minLat, _ := strconv.ParseFloat(q.Get("minLat"), 64)
	minLon, _ := strconv.ParseFloat(q.Get("minLon"), 64)
	maxLat, _ := strconv.ParseFloat(q.Get("maxLat"), 64)
	maxLon, _ := strconv.ParseFloat(q.Get("maxLon"), 64)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	signals, err := s.db.GetSignals(ctx, minLat, minLon, maxLat, maxLon)
	if err != nil {
		s.log.Error().Err(err).Msg("get signals")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, signals)
}

// handleGetSignal returns a single signal by ID.
func (s *Server) handleGetSignal(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id := chi.URLParam(r, "id")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	sig, err := s.db.GetSignal(ctx, id)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"signal not found"}`, http.StatusNotFound)
			return
		}
		s.log.Error().Err(err).Str("id", id).Msg("get signal")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, sig)
}

// handleGetRecordings returns recordings, optionally filtered by
// ?signalId= with ?limit=.
func (s *Server) handleGetRecordings(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	signalID := q.Get("signalId")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	recs, err := s.db.GetRecordings(ctx, limit, signalID)
	if err != nil {
		s.log.Error().Err(err).Msg("get recordings")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if recs == nil {
		recs = []db.Recording{}
	}
	writeJSON(w, recs)
}

// handleGetRecordingAudio serves a recorded audio file.
func (s *Server) handleGetRecordingAudio(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id := chi.URLParam(r, "id")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rec, err := s.db.GetRecording(ctx, id)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"recording not found"}`, http.StatusNotFound)
			return
		}
		s.log.Error().Err(err).Str("id", id).Msg("get recording")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Resolve safely inside the recordings directory.
	base, err := filepath.Abs(s.recordingsDir)
	if err != nil {
		http.Error(w, "invalid recordings dir", http.StatusInternalServerError)
		return
	}
	fpath := rec.FilePath
	if !filepath.IsAbs(fpath) {
		fpath = filepath.Join(base, fpath)
	}
	fpath = filepath.Clean(fpath)
	if !strings.HasPrefix(fpath, base+string(filepath.Separator)) {
		http.Error(w, `{"error":"invalid file path"}`, http.StatusNotFound)
		return
	}
	if _, err := os.Stat(fpath); err != nil {
		http.Error(w, `{"error":"file not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", audioContentType(rec.FileFormat))
	http.ServeFile(w, r, fpath)
}

// audioContentType maps a recordings.file_format to its HTTP
// Content-Type: only wav is decoded audio, everything else (iq) is
// raw bytes. FLAC is not supported (SPEC §10.5).
func audioContentType(format string) string {
	if format == "wav" {
		return "audio/wav"
	}
	return "application/octet-stream"
}

// handleGetSDRs returns all registered SDRs.
func (s *Server) handleGetSDRs(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	sdrs, err := s.db.ListSDRs(ctx)
	if err != nil {
		s.log.Error().Err(err).Msg("list sdrs")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sdrs == nil {
		sdrs = []db.SDRDevice{}
	}
	writeJSON(w, sdrs)
}

// sdrUpdate is a partial SDR settings update.
type sdrUpdate struct {
	Model  *string  `json:"model"`
	Serial *string  `json:"serial"`
	Lat    *float64 `json:"lat"`
	Lon    *float64 `json:"lon"`
	GainDB *float64 `json:"gainDb"`
	FreqHz *uint64  `json:"freqHz"`
	Active *bool    `json:"active"`
}

// handleUpdateSDR updates an SDR's settings.
func (s *Server) handleUpdateSDR(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id := chi.URLParam(r, "id")

	var upd sdrUpdate
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Load the current row and merge the partial update.
	existing, err := s.db.GetSDR(ctx, id)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"sdr not found"}`, http.StatusNotFound)
			return
		}
		s.log.Error().Err(err).Str("id", id).Msg("get sdr")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if upd.Model != nil {
		existing.Model = *upd.Model
	}
	if upd.Serial != nil {
		existing.Serial = *upd.Serial
	}
	if upd.Lat != nil {
		existing.Lat = *upd.Lat
	}
	if upd.Lon != nil {
		existing.Lon = *upd.Lon
	}
	if upd.GainDB != nil {
		existing.GainDB = *upd.GainDB
	}
	if upd.FreqHz != nil {
		existing.FreqHz = *upd.FreqHz
	}
	if upd.Active != nil {
		existing.Active = *upd.Active
	}

	if err := s.db.UpdateSDR(ctx, existing); err != nil {
		s.log.Error().Err(err).Str("id", id).Msg("update sdr")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, existing)
}

// relayUpgrader upgrades client WS requests at the gateway. Origin
// enforcement mirrors ws-hub (§17.2): cross-origin upgrades are
// rejected unless the origin is allowlisted; non-browser clients
// (no Origin header) pass.
var relayUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     ws.OriginCheckFunc(ws.AllowedOriginsFromEnv()),
}

// handleWSRelay upgrades the client connection and transparently
// relays frames in both directions between the client and the
// ws-hub /ws endpoint (§2.2, A3 — single client ingress). The hub is
// dialed first so an unreachable hub still answers the client's
// handshake with a plain **502 JSON** response.
func (s *Server) handleWSRelay(w http.ResponseWriter, r *http.Request) {
	if !ws.OriginCheckFunc(ws.AllowedOriginsFromEnv())(r) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"origin not allowed"}`, http.StatusForbidden)
		return
	}

	backend := url.URL{Scheme: "ws", Host: s.wsHubAddr, Path: "/ws"}
	hubConn, resp, err := websocket.DefaultDialer.Dial(backend.String(), nil)
	if err != nil {
		s.log.Error().Err(err).Str("hub", s.wsHubAddr).Msg("ws relay: hub unreachable")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"ws-hub unreachable"}`))
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return
	}

	clientConn, err := relayUpgrader.Upgrade(w, r, nil)
	if err != nil {
		hubConn.Close() // Upgrade already wrote the HTTP error
		return
	}

	clientDone := make(chan struct{})
	hubDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		for {
			mt, data, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := hubConn.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}()
	go func() {
		defer close(hubDone)
		for {
			mt, data, err := hubConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}()
	select {
	case <-clientDone:
	case <-hubDone:
	}
	clientConn.Close()
	hubConn.Close()
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}