package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
)

// relayAllowlist is the set of API calls a paired phone may make: the
// attention feed and the judgment actions on it, nothing else. Sessions,
// steering, settings, config writes, uploads and the events WebSocket are
// deliberately excluded — the phone client is for decisions, not operations.
var relayAllowlist = []struct{ method, pattern string }{
	{"GET", "/api/attention"},
	{"GET", "/api/version"},
	{"GET", "/api/reviews"},
	{"GET", "/api/audits"},
	{"GET", "/api/decisions"},
	{"GET", "/api/plans/{id}"},
	{"GET", "/api/tasks/{id}"},
	{"POST", "/api/decisions/{id}/answer"},
	{"POST", "/api/plans/{id}/approve"},
	{"POST", "/api/plans/{id}/reject"},
	{"POST", "/api/plans/{id}/revise"},
	{"POST", "/api/tasks/{id}/accept"},
	{"POST", "/api/tasks/{id}/reject"},
	{"POST", "/api/tasks/{id}/request-changes"},
	{"POST", "/api/tasks/{id}/audit-ok"},
	{"POST", "/api/tasks/{id}/revert"},
}

// allowed reports whether a relayed request may reach the API mux.
func allowed(method, path string) bool {
	for _, e := range relayAllowlist {
		if e.method == method && patternMatch(e.pattern, path) {
			return true
		}
	}
	return false
}

// patternMatch matches a /seg/{id}/seg pattern against a concrete path.
// {id} segments match any single non-empty segment.
func patternMatch(pattern, path string) bool {
	// Strip query/fragment; the mux would route on the path only anyway.
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	xs := strings.Split(strings.Trim(path, "/"), "/")
	if len(ps) != len(xs) {
		return false
	}
	for i := range ps {
		if strings.HasPrefix(ps[i], "{") && strings.HasSuffix(ps[i], "}") {
			if xs[i] == "" {
				return false
			}
			continue
		}
		if ps[i] != xs[i] {
			return false
		}
	}
	return true
}

// dispatchRPC replays an allowlisted phone request through the daemon's own
// API handler in-process and returns status + response body. Non-allowlisted
// calls are rejected without ever touching the mux.
func dispatchRPC(handler http.Handler, method, path string, body []byte) (int, json.RawMessage) {
	if handler == nil || !allowed(method, path) {
		b, _ := json.Marshal(map[string]string{"error": "not allowed over relay"})
		return http.StatusForbidden, b
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, json.RawMessage(rec.Body.Bytes())
}
