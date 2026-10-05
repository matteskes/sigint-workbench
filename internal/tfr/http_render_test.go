package tfr

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestTFRHappyPathSTFT(t *testing.T) {
	rec := writeIQ(t, toneIQ(1024, 1024, 32), 1024)
	ts := httpFixture(t, rec, true)
	defer ts.Close()
	resp := postTFR(t, ts, rec.ID, `{"method":"stft","nfft":256,"t1":1}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("happy path: want 200, got %d", resp.StatusCode)
	}
	var res Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Method != MethodStft || res.Window != DefaultWindow {
		t.Errorf("method/window: %s/%s", res.Method, res.Window)
	}
	if res.RecordingID != rec.ID || res.SampleRate != 1024 || res.CenterFreq != 146_000_000 {
		t.Errorf("metadata mismatch: %+v", res)
	}
	if len(res.Tile) != res.Rows*res.Cols || res.Rows == 0 || res.Cols == 0 {
		t.Fatalf("tile %dx%d len %d", res.Rows, res.Cols, len(res.Tile))
	}
	if res.Artifact == "" || res.DBStep != 1.0 {
		t.Errorf("artifact/dbStep: %q/%g", res.Artifact, res.DBStep)
	}
	if res.T0 != 0 || res.T1 != 1 || res.Overlap != DefaultOverlap {
		t.Errorf("span/overlap: %g..%g @%g", res.T0, res.T1, res.Overlap)
	}
	if res.ElapsedMS < 0 {
		t.Errorf("elapsedMs %d", res.ElapsedMS)
	}
}

func TestTFRHappyPathAllMethods(t *testing.T) {
	rec := writeIQ(t, toneIQ(1024, 2048, 32), 1024)
	ts := httpFixture(t, rec, true)
	defer ts.Close()
	for _, m := range []string{MethodStft, MethodReassigned, MethodSpwvd, MethodCwtMorlet} {
		body := `{"method":"` + m + `","nfft":256,"t1":2}`
		if m == MethodReassigned {
			body = `{"method":"reassigned","nfft":256,"t1":2,"window":"gaussian"}`
		}
		resp := postTFR(t, ts, rec.ID, body)
		var res Result
		err := json.NewDecoder(resp.Body).Decode(&res)
		code := resp.StatusCode
		resp.Body.Close()
		if code != http.StatusOK {
			t.Errorf("%s: want 200, got %d", m, code)
			continue
		}
		if err != nil {
			t.Errorf("%s: decode: %v", m, err)
			continue
		}
		if res.Method != m || res.Artifact != Artifact(m) {
			t.Errorf("%s: metadata wrong (method %s artifact %q)", m, res.Method, res.Artifact)
		}
		if len(res.Tile) == 0 {
			t.Errorf("%s: empty tile", m)
		}
	}
}

func TestTFRBodyCapStillRendersSmallSpans(t *testing.T) {
	// The 4 KiB body cap rejects oversized junk but real requests are
	// tiny — a full valid request with every optional field works.
	rec := writeIQ(t, toneIQ(1024, 1024, 32), 1024)
	ts := httpFixture(t, rec, true)
	defer ts.Close()
	resp := postTFR(t, ts, rec.ID,
		`{"method":"stft","t0":0,"t1":1,"nfft":256,"overlap":0.5,"window":"hamming","freqSpan":[145999990,146000042]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("full request: want 200, got %d", resp.StatusCode)
	}
	var res Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Overlap != 0.5 || res.Window != "hamming" {
		t.Errorf("optional fields not honored: overlap=%g window=%s", res.Overlap, res.Window)
	}
}
