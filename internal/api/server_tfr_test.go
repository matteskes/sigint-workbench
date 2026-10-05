package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"sigint-workbench/internal/db"
)

// newTFRTestServer wires a gateway whose only live dependency is the
// given recorder address; the DB handle is non-nil (requireDB passes)
// but never queried by the proxy.
func newTFRTestServer(recorderAPI string) *httptest.Server {
	srv := &Server{
		router:      chi.NewRouter(),
		log:         zerolog.Nop(),
		db:          &db.DB{},
		recorderAPI: recorderAPI,
	}
	srv.buildRoutes()
	return httptest.NewServer(srv.Handler())
}

func TestTFRProxyPassesStatusAndBodyThrough(t *testing.T) {
	// Stub recorder: answers 413 with the §19.3 span-cap error — the
	// gateway must relay status and body verbatim.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/recordings/rec-1/tfr" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"method":"stft","t1":31}` {
			t.Errorf("upstream body = %q", string(b))
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("upstream content-type = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":"span 31 s exceeds tfr.max_span_s 30"}` + "\n"))
	}))
	defer upstream.Close()

	ts := newTFRTestServer(strings.TrimPrefix(upstream.URL, "http://"))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/recordings/rec-1/tfr", "application/json",
		strings.NewReader(`{"method":"stft","t1":31}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413 passthrough, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "tfr.max_span_s") {
		t.Errorf("body not relayed: %q", string(body))
	}
}

func TestTFRProxy200Relayed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"method":"stft","rows":8,"cols":8,"tile":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]}`))
	}))
	defer upstream.Close()

	ts := newTFRTestServer(strings.TrimPrefix(upstream.URL, "http://"))
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/recordings/rec-1/tfr", "application/json",
		strings.NewReader(`{"method":"stft","nfft":256}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q", ct)
	}
}

func TestTFRProxyUnreachableIs502(t *testing.T) {
	ts := newTFRTestServer("127.0.0.1:1") // nothing listens there
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/recordings/rec-1/tfr", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "recorder unreachable") {
		t.Errorf("body = %q", string(body))
	}
}

func TestTFRProxyNoDBIs503(t *testing.T) {
	// §13 contract: every /api/* route answers 503 when the database
	// is down — before any proxying happens.
	log := zerolog.Nop()
	srv := NewServer(nil, log)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/recordings/rec-1/tfr", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}
