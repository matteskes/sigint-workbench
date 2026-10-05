// Package api provides the REST API and WebSocket server.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	recordingsDir  string
	wsHubAddr      string // ws-hub host:port for the /ws relay (§2.2)
	recorderWSAddr string // recorder host:port for the /ws/audio relay (§10.4)
	recorderAPI    string // recorder host:port for the TFR proxy (§19.3)
	captureCtrl    string // sdr-capture host:port for the control-API proxy (§7.4)
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
	recAddr := os.Getenv("RECORDER_WS_ADDR")
	if recAddr == "" {
		recAddr = "127.0.0.1:9012"
	}
	recAPI := os.Getenv("RECORDER_API_ADDR")
	if recAPI == "" {
		recAPI = "127.0.0.1:9013"
	}
	capAddr := os.Getenv("CAPTURE_CTRL_ADDR")
	if capAddr == "" {
		capAddr = "127.0.0.1:9090"
	}
	s := &Server{
		db:             database,
		log:            log,
		recordingsDir:  dir,
		wsHubAddr:      hubAddr,
		recorderWSAddr: recAddr,
		recorderAPI:    recAPI,
		captureCtrl:    capAddr,
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
	s.router.Get("/api/signals/{id}/annotations", s.handleGetAnnotations)
	s.router.Post("/api/signals/{id}/annotations", s.handleAddAnnotation)
	s.router.Get("/api/signals/{id}/track", s.handleGetSignalTrack)

	// Recordings
	s.router.Get("/api/recordings", s.handleGetRecordings)
	s.router.Get("/api/recordings/{id}/audio", s.handleGetRecordingAudio)
	s.router.Post("/api/recordings/{id}/tfr", s.handleRecordingTFR)

	// SDRs
	s.router.Get("/api/sdrs", s.handleGetSDRs)
	s.router.Put("/api/sdrs/{id}", s.handleUpdateSDR)

	// WebSocket relays (§2.2, A3): the gateway is the single client
	// ingress; the former in-process hub had no producers and was
	// removed (§13.2.4). /ws relays signal events from ws-hub;
	// /ws/audio relays the live Opus stream from the recorder (§10.4).
	s.router.Get("/ws", s.handleWSRelay)
	s.router.Get("/ws/audio", s.handleWSAudioRelay)

	// SDRs
	s.router.Get("/api/sdrs", s.handleGetSDRs)
	s.router.Put("/api/sdrs/{id}", s.handleUpdateSDR)
	s.router.Get("/api/sdrs/{id}/status", s.handleGetSDRStatus)
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

// handleGetAnnotations lists a signal's user notes, newest first
// (§12.5). Unknown signals and known ones both answer the same way —
// an empty list — because the query filters by signal_id directly.
func (s *Server) handleGetAnnotations(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id := chi.URLParam(r, "id")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	notes, err := s.db.GetAnnotations(ctx, id)
	if err != nil {
		s.log.Error().Err(err).Str("id", id).Msg("get annotations")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if notes == nil {
		notes = []db.Annotation{}
	}
	writeJSON(w, notes)
}

// annotationRequest is the POST /api/signals/{id}/annotations body.
type annotationRequest struct {
	UserNote *string `json:"userNote"`
}

// handleAddAnnotation appends a user note to a signal (§12.5). The
// body is validated before requireDB so malformed requests answer 400
// even when the database is down; unknown signals ⇒ 404.
func (s *Server) handleAddAnnotation(w http.ResponseWriter, r *http.Request) {
	var req annotationRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	var note string
	if req.UserNote != nil {
		note = strings.TrimSpace(*req.UserNote)
	}
	if note == "" {
		http.Error(w, `{"error":"userNote required"}`, http.StatusBadRequest)
		return
	}
	if !s.requireDB(w) {
		return
	}
	id := chi.URLParam(r, "id")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	a, err := s.db.AddAnnotation(ctx, id, note)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"signal not found"}`, http.StatusNotFound)
			return
		}
		s.log.Error().Err(err).Str("id", id).Msg("add annotation")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, a)
}

// handleGetSignalTrack returns a signal's current track (§9.4, §12.4):
// path, speed and heading. Signals without a track answer 404.
func (s *Server) handleGetSignalTrack(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id := chi.URLParam(r, "id")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	track, err := s.db.GetTrack(ctx, id)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"track not found"}`, http.StatusNotFound)
			return
		}
		s.log.Error().Err(err).Str("id", id).Msg("get track")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, track)
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

// tfrProxyTimeout bounds the synchronous §19.3 render: request cost
// is bounded by the §19.5 caps, so a generous fixed ceiling covers
// the worst in-cap render without ever queueing (§19.3).
const tfrProxyTimeout = 120 * time.Second

// handleRecordingTFR proxies POST /api/recordings/{id}/tfr to the
// recorder (§19.3, §13.1/A3 — single client ingress). The body is
// forwarded untouched and the upstream status passes through, so the
// §19.3 contract (400/404/413, disabled ⇒ 404) is preserved verbatim;
// an unreachable recorder answers 502. Like every /api/* route the
// gateway answers 503 when the database is down.
func (s *Server) handleRecordingTFR(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id := chi.URLParam(r, "id")
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, `{"error":"could not read request body"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), tfrProxyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+s.recorderAPI+"/api/recordings/"+url.PathEscape(id)+"/tfr",
		bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":"tfr proxy request failed"}`, http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Error().Err(err).Str("id", id).Str("upstream", s.recorderAPI).Msg("tfr proxy: recorder unreachable")
		http.Error(w, `{"error":"recorder unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
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

	// Forward hardware-affecting fields to the capture control API
	// (§7.4) so freqHz/gainDb actually retune the device. The DB row
	// above is already saved; a control-API failure surfaces as 502
	// with a clear body — never silently DB-only (§13.2.3).
	if upd.FreqHz != nil || upd.GainDB != nil {
		if fail := s.retuneCapture(ctx, id, upd.FreqHz, upd.GainDB); fail != nil {
			s.log.Error().Str("id", id).Msg("capture retune: " + fail.message)
			body, _ := json.Marshal(map[string]string{"error": fail.message})
			http.Error(w, string(body), fail.status)
			return
		}
	}
	writeJSON(w, existing)
}

// ctrlFailure is a control-API failure carrying the HTTP status the
// gateway should surface to the client (§13.2.3: control-API
// failures must never be swallowed into a DB-only update).
type ctrlFailure struct {
	status  int
	message string
}

func (e *ctrlFailure) Error() string { return e.message }

// captureFreqRequest is the body of the capture control API's
// POST /api/v1/frequency (§7.4).
type captureFreqRequest struct {
	ID      string  `json:"id"`
	FreqMHz float64 `json:"freq_mhz"`
}

// captureGainRequest is the body of the capture control API's
// POST /api/v1/gain (§7.4).
type captureGainRequest struct {
	ID     string  `json:"id"`
	GainDB float64 `json:"gain_db"`
}

// retuneCapture forwards hardware-affecting fields of an SDR update
// to the sdr-capture control API (§7.4): freqHz (DB units, Hz) is
// converted to the control API's MHz. A manual frequency command
// pauses that device's scan loop until the capture service restarts
// (§7.4, documented operator behavior).
func (s *Server) retuneCapture(ctx context.Context, id string, freqHz *uint64, gainDb *float64) *ctrlFailure {
	post := func(path string, payload any) *ctrlFailure {
		buf, err := json.Marshal(payload)
		if err != nil {
			return &ctrlFailure{http.StatusInternalServerError, "retune request encode failed"}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"http://"+s.captureCtrl+path, bytes.NewReader(buf))
		if err != nil {
			return &ctrlFailure{http.StatusBadGateway, "sdr-capture unreachable"}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return &ctrlFailure{http.StatusBadGateway,
				"sdr-capture unreachable (DB row updated; hardware not retuned)"}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return &ctrlFailure{http.StatusNotFound, "sdr-capture reports unknown SDR id"}
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			s.log.Error().Str("id", id).Str("path", path).
				Int("status", resp.StatusCode).Msg("capture control: " + string(b))
			return &ctrlFailure{http.StatusBadGateway, "sdr-capture control API error"}
		}
		return nil
	}
	if freqHz != nil {
		if fail := post("/api/v1/frequency", captureFreqRequest{
			ID:      id,
			FreqMHz: float64(*freqHz) / 1e6,
		}); fail != nil {
			return fail
		}
	}
	if gainDb != nil {
		if fail := post("/api/v1/gain", captureGainRequest{ID: id, GainDB: *gainDb}); fail != nil {
			return fail
		}
	}
	return nil
}

// handleGetSDRStatus proxies the capture control API's per-device
// live state (§7.4, §13.1). The data is live from sdr-capture, not
// the DB, but per the §13 contract every /api/* route answers 503
// when the DB is down.
func (s *Server) handleGetSDRStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	id := chi.URLParam(r, "id")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status, fail := s.captureStatus(ctx, id)
	if fail != nil {
		s.log.Error().Str("id", id).Msg("capture status: " + fail.message)
		body, _ := json.Marshal(map[string]string{"error": fail.message})
		http.Error(w, string(body), fail.status)
		return
	}
	writeJSON(w, status)
}

// captureStatus fetches GET /api/v1/status from the capture control
// API and returns the entry matching the device id.
func (s *Server) captureStatus(ctx context.Context, id string) (map[string]any, *ctrlFailure) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+s.captureCtrl+"/api/v1/status", nil)
	if err != nil {
		return nil, &ctrlFailure{http.StatusBadGateway, "sdr-capture unreachable"}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, &ctrlFailure{http.StatusBadGateway, "sdr-capture unreachable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &ctrlFailure{http.StatusBadGateway, "sdr-capture control API error"}
	}
	var all []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, &ctrlFailure{http.StatusBadGateway,
			"sdr-capture control API returned invalid JSON"}
	}
	for _, m := range all {
		if devID, _ := m["id"].(string); devID == id {
			return m, nil
		}
	}
	return nil, &ctrlFailure{http.StatusNotFound, "sdr not found in capture status"}
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

// handleWSRelay relays the client WebSocket to the ws-hub /ws
// endpoint (§2.2, A3 — single client ingress).
func (s *Server) handleWSRelay(w http.ResponseWriter, r *http.Request) {
	if !s.checkRelayOrigin(w, r) {
		return
	}
	backend := url.URL{Scheme: "ws", Host: s.wsHubAddr, Path: "/ws"}
	s.relayWS(w, r, backend, "ws-hub", s.wsHubAddr)
}

// handleWSAudioRelay relays GET /ws/audio?signal=<id> to the
// recorder's internal live-audio WS (§10.4): the query string is
// forwarded untouched; after the handshake the client receives the
// recorder's one text audio.meta hello followed by binary Opus
// packets, all passed through without decoding.
func (s *Server) handleWSAudioRelay(w http.ResponseWriter, r *http.Request) {
	if !s.checkRelayOrigin(w, r) {
		return
	}
	backend := url.URL{
		Scheme:   "ws",
		Host:     s.recorderWSAddr,
		Path:     "/ws/audio",
		RawQuery: r.URL.RawQuery,
	}
	s.relayWS(w, r, backend, "recorder", s.recorderWSAddr)
}

// checkRelayOrigin enforces the same origin allowlist as ws-hub
// (§17.2): foreign origins get 403, non-browser clients (no Origin
// header) pass.
func (s *Server) checkRelayOrigin(w http.ResponseWriter, r *http.Request) bool {
	if ws.OriginCheckFunc(ws.AllowedOriginsFromEnv())(r) {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, `{"error":"origin not allowed"}`, http.StatusForbidden)
	return false
}

// relayWS dials the upstream before upgrading the client — an
// unreachable upstream answers the client's handshake with a plain
// **502 JSON** response — then transparently pumps frames in both
// directions until either side closes.
func (s *Server) relayWS(w http.ResponseWriter, r *http.Request, backend url.URL, name, addr string) {
	up, resp, err := websocket.DefaultDialer.Dial(backend.String(), nil)
	if err != nil {
		s.log.Error().Err(err).Str("upstream", addr).Msg("ws relay: " + name + " unreachable")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprintf(w, `{"error":%q}`, name+" unreachable")
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return
	}

	clientConn, err := relayUpgrader.Upgrade(w, r, nil)
	if err != nil {
		up.Close() // Upgrade already wrote the HTTP error
		return
	}

	clientDone := make(chan struct{})
	upDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		for {
			mt, data, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := up.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}()
	go func() {
		defer close(upDone)
		for {
			mt, data, err := up.ReadMessage()
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
	case <-upDone:
	}
	clientConn.Close()
	up.Close()
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
