package tfr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"sigint-workbench/internal/db"
)

// Lookup resolves a recording id. Production wires db.DB.GetRecording;
// tests stub it. Errors of type db.ErrNotFound mean "unknown
// recording" (§19.3 ⇒ 404).
type Lookup func(ctx context.Context, id string) (*db.Recording, error)

// statusError lets compute-side failures declare their §19.3 status.
type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

// HTTPStatus reports the response code the handler should use.
func (e *statusError) HTTPStatus() int { return e.status }

// spanClampErr satisfies statusError (§19.3: 413).
func (e spanClampErr) HTTPStatus() int { return http.StatusRequestEntityTooLarge }

// paramError is a §19.3 400 (invalid params).
func paramErr(format string, args ...any) *statusError {
	return &statusError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, args...)}
}

// maxBodyBytes bounds the request body; §19.3 bodies are tiny.
const maxBodyBytes = 4 << 10

// Handler serves POST /api/recordings/{id}/tfr for the recorder
// (§19.3). Mounted internal-only (§3.2); the api-gateway proxies it
// (§13.1, A3). Synchronous only — no queueing, no background jobs.
type Handler struct {
	limits Limits
	lookup Lookup
}

// NewHandler builds the recorder's TFR endpoint.
func NewHandler(limits Limits, lookup Lookup) *Handler {
	if limits.MaxSpanS <= 0 {
		limits.MaxSpanS = DefaultMaxSpan
	}
	if limits.MaxNFFT <= 0 {
		limits.MaxNFFT = DefaultMaxNFFT
	}
	return &Handler{limits: limits, lookup: lookup}
}

// Routes mounts the endpoint (plus a liveness probe) on a chi router.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Post("/api/recordings/{id}/tfr", h.handleTFR)
	return r
}

// handleTFR implements the §19.3 contract in order: feature
// disabled ⇒ 404 (feature absent, not an error state); invalid JSON
// ⇒ 400; unknown recording or no raw IQ ⇒ 404; invalid params ⇒ 400;
// span beyond tfr.max_span_s ⇒ 413; success ⇒ 200 tiles + metadata.
func (h *Handler) handleTFR(w http.ResponseWriter, r *http.Request) {
	if !h.limits.Enabled {
		writeError(w, http.StatusNotFound, "tfr feature disabled")
		return
	}
	id := chi.URLParam(r, "id")
	var req Request
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	rec, err := h.lookup(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "recording lookup failed")
		return
	}
	if rec == nil || rec.FileFormat != "iq" {
		// §19 computes over stored raw IQ (§10.5/§11.3); a recording
		// without an .iq side file has nothing to analyze.
		writeError(w, http.StatusNotFound, "recording has no raw IQ")
		return
	}
	if _, err := os.Stat(rec.FilePath); err != nil {
		// Retention (§11.3) may have removed the file already.
		writeError(w, http.StatusNotFound, "recording file unavailable")
		return
	}

	// Validate first so param errors answer before any I/O beyond the
	// stat — and so span clamping (413) is precise.
	if _, verr := req.validate(h.limits, rec.DurationS); verr != nil {
		var se interface{ HTTPStatus() int }
		if errors.As(verr, &se) {
			writeError(w, se.HTTPStatus(), verr.Error())
			return
		}
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	}

	res, err := Compute(rec, req, h.limits)
	if err != nil {
		var se interface{ HTTPStatus() int }
		if errors.As(err, &se) {
			writeError(w, se.HTTPStatus(), err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "tfr computation failed")
		return
	}
	writeJSON(w, res)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`+"\n", msg)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
