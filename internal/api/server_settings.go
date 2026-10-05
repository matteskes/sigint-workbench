// Package api — §20 setup screen endpoints: settings schema/snapshot
// reads, validated section saves, first-run state, and the component
// probes behind GET /api/setup/status.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"sigint-workbench/internal/db"
	"sigint-workbench/internal/settings"
)

// setupCompletedKey marks in app_settings that the §20 wizard has
// been completed (or dismissed) at least once. Empty = not completed.
const setupCompletedKey = "setup.completed_at"

// componentStatus is one GET /api/setup/status probe result.
type componentStatus struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// handleSetupStatus probes db, ws-hub, recorder and capture
// concurrently, each capped at 2 s (the documented §13 exception:
// the gateway fans out instead of proxying, so first-run users see
// every unhealthy service at once, not one failure at a time).
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	out := map[string]componentStatus{
		"db":       {},
		"ws-hub":   {},
		"recorder": {},
		"capture":  {},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	probe := func(name string, fn func() componentStatus) {
		defer wg.Done()
		c := fn()
		mu.Lock()
		out[name] = c
		mu.Unlock()
	}
	wg.Add(4)
	go probe("db", func() componentStatus { return s.probeDB(ctx) })
	go probe("ws-hub", func() componentStatus { return probeTCP(ctx, s.wsHubAddr) })
	go probe("recorder", func() componentStatus { return probeTCP(ctx, s.recorderAPI) })
	go probe("capture", func() componentStatus { return probeTCP(ctx, s.captureCtrl) })
	wg.Wait()

	allOK := true
	for _, c := range out {
		if !c.OK {
			allOK = false
			break
		}
	}
	writeJSON(w, map[string]any{
		"components":          out,
		"all_ok":              allOK,
		"config_dir_writable": s.settings.Available() == nil,
	})
}

// probeDB pings the database within the probe budget.
func (s *Server) probeDB(ctx context.Context) componentStatus {
	if s.db == nil {
		return componentStatus{Detail: "database not configured"}
	}
	if err := s.db.Pool.Ping(ctx); err != nil {
		return componentStatus{Detail: "unreachable"}
	}
	return componentStatus{OK: true}
}

// probeTCP checks plain TCP reachability of an internal service.
func probeTCP(ctx context.Context, addr string) componentStatus {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return componentStatus{Detail: "unreachable"}
	}
	_ = conn.Close()
	return componentStatus{OK: true}
}

// handleSettingsIndex serves the whole editable surface at once: the
// server-authoritative schema plus the current per-section values
// (nil for sections whose file is missing or unparseable, §20).
func (s *Server) handleSettingsIndex(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"sections": settings.Schema(),
		"values":   s.settings.Snapshot(),
	})
}

// handleSettingsGet serves one section schema plus its current values.
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "section")
	sec, ok := settings.SectionByID(id)
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "unknown section: "+id, "")
		return
	}
	writeJSON(w, map[string]any{
		"section": sec,
		"values":  s.settings.Snapshot()[id],
	})
}

// handleSettingsPut validates and applies one section save. Field
// validation failures answer 400 with {error, field}; unknown
// sections 404; anything else 500 with details logged.
func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "section")
	var body struct {
		Values map[string]any `json:"values"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid JSON body", "")
		return
	}
	res, err := s.settings.Save(id, body.Values)
	if err != nil {
		var ve *settings.ValidationError
		switch {
		case errors.As(err, &ve):
			writeErrorJSON(w, http.StatusBadRequest, ve.Err.Error(), ve.Field)
		case errors.Is(err, settings.ErrUnknownSection):
			writeErrorJSON(w, http.StatusNotFound, err.Error(), "")
		default:
			s.log.Error().Err(err).Str("section", id).Msg("settings save failed")
			writeErrorJSON(w, http.StatusInternalServerError, "save failed", "")
		}
		return
	}
	s.log.Info().Str("section", res.Section).Str("file", res.File).
		Strs("restart", res.Restart).Msg("settings saved; restart to apply")
	writeJSON(w, map[string]any{
		"result": res,
		"values": s.settings.Snapshot()[id],
	})
}

// handleSetupStateGet reports the §20 first-run flag: true until the
// wizard has been completed (or dismissed) at least once.
func (s *Server) handleSetupStateGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	v, err := s.db.GetSetting(r.Context(), setupCompletedKey)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		s.log.Error().Err(err).Msg("setup state read failed")
		writeErrorJSON(w, http.StatusInternalServerError, "setup state read failed", "")
		return
	}
	writeJSON(w, map[string]any{"first_run": v == ""})
}

// handleSetupStatePut completes (first_run=false) or re-arms
// (first_run=true) the §20 wizard.
func (s *Server) handleSetupStatePut(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	var body struct {
		FirstRun *bool `json:"first_run"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.FirstRun == nil {
		writeErrorJSON(w, http.StatusBadRequest, "body must be {\"first_run\": bool}", "")
		return
	}
	value := time.Now().UTC().Format(time.RFC3339)
	if *body.FirstRun {
		value = "" // re-arm: first_run flips back to true
	}
	if err := s.db.SetSetting(r.Context(), setupCompletedKey, value); err != nil {
		s.log.Error().Err(err).Msg("setup state write failed")
		writeErrorJSON(w, http.StatusInternalServerError, "setup state write failed", "")
		return
	}
	writeJSON(w, map[string]any{"first_run": *body.FirstRun})
}

// writeErrorJSON answers {"error": ..., "field": ...}; §20 field
// validation errors carry the offending dotted key.
func writeErrorJSON(w http.ResponseWriter, status int, msg, field string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]any{"error": msg}
	if field != "" {
		payload["field"] = field
	}
	_ = json.NewEncoder(w).Encode(payload)
}
