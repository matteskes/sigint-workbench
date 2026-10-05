package tfr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"sigint-workbench/internal/db"
)

// httpFixture wires a handler over one fixture recording with a stub
// lookup, and returns the test server.
func httpFixture(t *testing.T, rec *db.Recording, enabled bool) *httptest.Server {
	t.Helper()
	lookup := func(ctx context.Context, id string) (*db.Recording, error) {
		if rec != nil && id == rec.ID {
			return rec, nil
		}
		return nil, db.ErrNotFound
	}
	h := NewHandler(Limits{Enabled: enabled, MaxSpanS: 30, MaxNFFT: 16384}, lookup)
	return httptest.NewServer(h.Routes())
}

// postTFR posts a body to the fixture endpoint.
func postTFR(t *testing.T, server *httptest.Server, id, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(server.URL+"/api/recordings/"+id+"/tfr", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestTFRDisabledIs404(t *testing.T) {
	rec := writeIQ(t, toneIQ(1024, 1024, 32), 1024)
	ts := httpFixture(t, rec, false)
	defer ts.Close()

	// Feature absent ⇒ 404 regardless of body validity (§19.3), and
	// even for an unknown id — the feature simply is not there.
	resp := postTFR(t, ts, rec.ID, `{"method":"stft","nfft":256,"t1":1}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("disabled: want 404, got %d", resp.StatusCode)
	}
	resp2 := postTFR(t, ts, "nope", `{invalid json`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("disabled+bad body: want 404, got %d", resp2.StatusCode)
	}
}

func TestTFRContract(t *testing.T) {
	rec := writeIQ(t, toneIQ(1024, 1024, 32), 1024) // 1 s
	ts := httpFixture(t, rec, true)
	defer ts.Close()

	cases := []struct {
		name string
		id   string
		body string
		want int
	}{
		{"unknown recording", "00000000-0000-0000-0000-000000000000", `{"method":"stft","nfft":256,"t1":1}`, 404},
		{"bad json", rec.ID, `{nope`, 400},
		{"unknown method", rec.ID, `{"method":"choi-williams","nfft":256,"t1":1}`, 400},
		{"nfft below floor", rec.ID, `{"method":"stft","nfft":128,"t1":1}`, 400},
		{"overlap 1", rec.ID, `{"method":"stft","nfft":256,"t1":1,"overlap":1}`, 400},
		{"bogus window", rec.ID, `{"method":"stft","nfft":256,"t1":1,"window":"flatop"}`, 400},
		{"reassigned rectangular", rec.ID, `{"method":"reassigned","nfft":256,"t1":1,"window":"rectangular"}`, 400},
		{"t1 before t0", rec.ID, `{"method":"stft","nfft":256,"t0":0.9,"t1":0.1}`, 400},
		{"t1 beyond duration", rec.ID, `{"method":"stft","nfft":256,"t1":5}`, 400},
	}
	for _, tc := range cases {
		resp := postTFR(t, ts, tc.id, tc.body)
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: want %d, got %d (%s)", tc.name, tc.want, resp.StatusCode, string(body[:n]))
		}
	}
}

func TestTFRSpanCapIs413(t *testing.T) {
	long := writeIQ(t, toneIQ(1000, 31000, 100), 1000) // 31 s row
	ts := httpFixture(t, long, true)
	defer ts.Close()
	resp := postTFR(t, ts, long.ID, `{"method":"stft","nfft":256,"t0":0,"t1":31}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("span beyond cap: want 413, got %d", resp.StatusCode)
	}
}

func TestTFRMissingFileIs404(t *testing.T) {
	rec := writeIQ(t, toneIQ(1024, 1024, 32), 1024)
	if err := os.Remove(rec.FilePath); err != nil {
		t.Fatal(err)
	}
	ts := httpFixture(t, rec, true)
	defer ts.Close()
	resp := postTFR(t, ts, rec.ID, `{"method":"stft","nfft":256,"t1":1}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("purged file: want 404, got %d", resp.StatusCode)
	}
}

func TestTFRWAVOnlyIs404(t *testing.T) {
	rec := writeIQ(t, toneIQ(1024, 1024, 32), 1024)
	rec.FileFormat = "wav"
	ts := httpFixture(t, rec, true)
	defer ts.Close()
	resp := postTFR(t, ts, rec.ID, `{"method":"stft","nfft":256,"t1":1}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("wav-only: want 404, got %d", resp.StatusCode)
	}
}
