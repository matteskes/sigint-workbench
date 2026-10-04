package ws

import (
	"net/http"
	"testing"
)

func originReq(origin, host string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://"+host+"/ws", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestOriginAllowed(t *testing.T) {
	allowed := []string{"http://localhost:3000", "http://localhost:5173"}

	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"no origin header (non-browser client)", "", "ws-hub:8081", true},
		{"same origin", "http://ws-hub:8081", "ws-hub:8081", true},
		{"allowlisted dev frontend", "http://localhost:5173", "ws-hub:8081", true},
		{"allowlisted prod frontend", "http://localhost:3000", "ws-hub:8081", true},
		{"foreign origin", "http://evil.example", "ws-hub:8081", false},
		{"allowlisted authority, wrong scheme", "https://localhost:5173", "ws-hub:8081", false},
		{"malformed origin", "::::", "ws-hub:8081", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CheckOrigin(allowed, originReq(tc.origin, tc.host), tc.origin); got != tc.want {
				t.Fatalf("CheckOrigin(origin=%q, host=%q) = %v, want %v", tc.origin, tc.host, got, tc.want)
			}
		})
	}
}

func TestOriginCheckFunc(t *testing.T) {
	check := OriginCheckFunc([]string{"http://localhost:5173"})
	if !check(originReq("", "hub:8081")) {
		t.Fatal("no-origin request must pass")
	}
	if !check(originReq("http://hub:8081", "hub:8081")) {
		t.Fatal("same-origin request must pass")
	}
	if !check(originReq("http://localhost:5173", "hub:8081")) {
		t.Fatal("allowlisted origin must pass")
	}
	if check(originReq("http://evil.example", "hub:8081")) {
		t.Fatal("foreign origin must be rejected")
	}
}

func TestAllowedOriginsFromEnv(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", " http://a.example ,http://b.example,")
	got := AllowedOriginsFromEnv()
	if len(got) != 2 || got[0] != "http://a.example" || got[1] != "http://b.example" {
		t.Fatalf("AllowedOriginsFromEnv() = %v, want 2 trimmed entries", got)
	}

	t.Setenv("ALLOWED_ORIGINS", "")
	if got := AllowedOriginsFromEnv(); len(got) != 0 {
		t.Fatalf("set-but-empty must deny all cross-origin, got %v", got)
	}
}

func TestAllowedOriginsDefaultWhenUnset(t *testing.T) {
	orig := lookupEnv
	lookupEnv = func(string) (string, bool) { return "", false }
	defer func() { lookupEnv = orig }()

	got := AllowedOriginsFromEnv()
	if len(got) != 2 || got[0] != "http://localhost:3000" || got[1] != "http://localhost:5173" {
		t.Fatalf("default origins = %v", got)
	}
}