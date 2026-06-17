package api

import (
	"net/http/httptest"
	"testing"
)

// TestGuardOrigin verifies the same-origin / loopback-Host policy that protects
// the loopback-bound API from CSRF and DNS-rebinding (see guardOrigin).
func TestGuardOrigin(t *testing.T) {
	h := newTestServer(t)

	cases := []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{"loopback host, no origin", "localhost", "", 200},
		{"127.0.0.1 host", "127.0.0.1:7777", "", 200},
		{"same-origin write", "localhost:7777", "http://localhost:7777", 200},
		{"127.0.0.1 origin", "localhost", "http://127.0.0.1:7777", 200},
		{"cross-origin (DNS rebinding write)", "localhost", "http://evil.com", 403},
		{"non-loopback host (DNS rebinding read)", "evil.com", "", 403},
		{"non-loopback host with matching origin", "evil.com:7777", "http://evil.com:7777", 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/agents", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("Host=%q Origin=%q: got %d, want %d", tc.host, tc.origin, rec.Code, tc.want)
			}
		})
	}
}
