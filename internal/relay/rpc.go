package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
)

// relayAllowlist is the set of API calls a paired phone may make: the
// attention feed, the judgment actions on it, the full task list (the board's
// lifecycle columns), defining/grooming the backlog, the per-task comment
// thread, managing the agent registry (list/create/update/delete and
// enable/disable), and shipping the branch. Sessions, steering,
// settings, config writes, uploads and the events WebSocket are deliberately
// excluded — the phone is for judgment and kicking off work, not full
// operations.
var relayAllowlist = []struct{ method, pattern string }{
	{"GET", "/api/attention"},
	{"GET", "/api/version"},
	{"GET", "/api/reviews"},
	{"GET", "/api/audits"},
	{"GET", "/api/decisions"},
	{"GET", "/api/plans/{id}"},
	{"GET", "/api/tasks"}, // list (board's lifecycle columns: ready→closed)
	{"GET", "/api/tasks/{id}"},
	{"GET", "/api/tasks/{id}/comments"},  // read a task's conversation thread
	{"POST", "/api/tasks/{id}/comments"}, // add a note to a task
	{"GET", "/api/bigtasks"},             // list (phone filters to the backlog)
	{"POST", "/api/bigtasks"},            // define a big task (Create & plan / Backlog)
	{"POST", "/api/bigtasks/{id}/plan"},  // promote a backlog item into planning
	{"DELETE", "/api/bigtasks/{id}"},     // drop a backlog item from the phone
	{"GET", "/api/agents"},               // list the agent registry
	{"POST", "/api/agents"},              // register a new agent
	{"PUT", "/api/agents/{id}"},          // edit an agent
	{"DELETE", "/api/agents/{id}"},       // remove an agent
	{"POST", "/api/agents/{id}/enable"},  // enable an agent
	{"POST", "/api/agents/{id}/disable"}, // disable an agent
	{"POST", "/api/decisions/{id}/answer"},
	{"POST", "/api/plans/{id}/approve"},
	{"POST", "/api/plans/{id}/reject"},
	{"POST", "/api/plans/{id}/revise"},
	{"POST", "/api/tasks/{id}/accept"},
	{"POST", "/api/tasks/{id}/reject"},
	{"POST", "/api/tasks/{id}/retry"},
	{"POST", "/api/tasks/{id}/request-changes"},
	{"POST", "/api/tasks/{id}/audit-ok"},
	{"POST", "/api/tasks/{id}/revert"},
	{"POST", "/api/git/push"}, // ship the integration branch to its remote
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
