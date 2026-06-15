package relay

import (
	"net/http"
	"testing"
)

func TestAllowlist(t *testing.T) {
	allow := []struct{ method, path string }{
		{"GET", "/api/attention"},
		{"GET", "/api/tasks"},
		{"GET", "/api/tasks/abc-123"},
		{"GET", "/api/bigtasks"},
		{"POST", "/api/bigtasks"},
		{"POST", "/api/bigtasks/b1/plan"},
		{"POST", "/api/decisions/d1/answer"},
		{"POST", "/api/plans/p1/revise"},
		{"POST", "/api/tasks/t1/accept"},
		{"POST", "/api/tasks/t1/retry"},
		{"POST", "/api/tasks/t1/audit-ok"},
		{"GET", "/api/tasks/t1/comments"},
		{"POST", "/api/tasks/t1/comments"},
		{"DELETE", "/api/bigtasks/b1"}, // backlog grooming from the phone
		{"POST", "/api/git/push"},      // ship the branch from the phone
		{"GET", "/api/attention?x=1"},  // query strings don't bypass
	}
	for _, c := range allow {
		if !allowed(c.method, c.path) {
			t.Errorf("%s %s should be allowed", c.method, c.path)
		}
	}

	deny := []struct{ method, path string }{
		{"POST", "/api/attention"}, // wrong method
		{"GET", "/api/settings"},   // settings are local-only
		{"PUT", "/api/settings"},
		{"GET", "/api/sessions"}, // chat sessions are local-only
		{"POST", "/api/sessions/s1/messages"},
		{"POST", "/api/steer"},
		{"POST", "/api/push"},  // phone ships via /api/git/push instead
		{"GET", "/api/events"}, // events flow via forwarding
		{"DELETE", "/api/tasks/t1"},
		{"POST", "/api/bigtasks/b1/comments"},  // bigtask comments are local-only
		{"POST", "/api/bigtasks/b1/stop"},      // stopping work is local-only
		{"POST", "/api/bigtasks/reorder"},      // board ordering is local-only
		{"POST", "/api/tasks/t1/accept/extra"}, // extra segment
		{"POST", "/api/relay/pair"},            // no relay-ception
	}
	for _, c := range deny {
		if allowed(c.method, c.path) {
			t.Errorf("%s %s should be denied", c.method, c.path)
		}
	}
}

func TestDispatchRPCWithoutHandler(t *testing.T) {
	status, _ := dispatchRPC(nil, "GET", "/api/attention", nil)
	if status != http.StatusForbidden {
		t.Fatalf("nil handler should 403, got %d", status)
	}
}
