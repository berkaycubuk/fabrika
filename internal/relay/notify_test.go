package relay

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// recordingTransport captures push service requests and returns a canned status.
type recordingTransport struct {
	mu     sync.Mutex
	status int
	posts  []string // endpoint URLs hit
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.posts = append(rt.posts, r.URL.String())
	status := rt.status
	rt.mu.Unlock()
	rec := &http.Response{StatusCode: status, Body: http.NoBody, Header: http.Header{}}
	return rec, nil
}

func (rt *recordingTransport) count() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.posts)
}

func newTestNotifier(t *testing.T) (*Notifier, *Manager, *fakeHub, *recordingTransport, string) {
	t.Helper()
	hub := &fakeHub{}
	m, st := newTestManager(t, "http://unused.invalid", hub)
	_, daemonID, err := m.ensureIdentity()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	d := &model.RelayDevice{DaemonID: daemonID, PubKey: make([]byte, 32), Name: "p"}
	if err := st.Relay.AddDevice(d); err != nil {
		t.Fatal(err)
	}
	if err := st.Relay.AddPushSub(&model.RelayPushSub{
		Endpoint: "https://push.example/sub-1",
		DaemonID: daemonID,
		DeviceID: d.ID,
		// Real (random) keys so webpush-go's payload encryption succeeds.
		P256DH: "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM",
		Auth:   "tBHItJI5svbpez7KI4CCXg",
	}); err != nil {
		t.Fatal(err)
	}

	rt := &recordingTransport{status: http.StatusCreated}
	n := NewNotifier(st, m, hub.subscribe)
	n.debounce = 30 * time.Millisecond
	n.client = &http.Client{Transport: rt}
	n.logf = t.Logf
	return n, m, hub, rt, daemonID
}

func waitCount(t *testing.T, rt *recordingTransport, want int) {
	t.Helper()
	waitFor(t, "push count", func() bool { return rt.count() == want })
}

func TestNotifierDecisionTriggersPush(t *testing.T) {
	n, _, hub, rt, _ := newTestNotifier(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.Start(ctx)

	hub.broadcast(Event{Type: "decision.created", Payload: model.Decision{ID: "d1"}})
	waitCount(t, rt, 1)

	// Debounce: a burst inside one window sends exactly one push.
	hub.broadcast(Event{Type: "decision.created", Payload: model.Decision{ID: "d2"}})
	hub.broadcast(Event{Type: "plan.ready", Payload: model.Plan{}})
	waitCount(t, rt, 2)
	time.Sleep(3 * n.debounce)
	if rt.count() != 2 {
		t.Fatalf("burst should debounce to one push, got %d total", rt.count())
	}
}

func TestNotifierTaskTransitions(t *testing.T) {
	n, _, hub, rt, _ := newTestNotifier(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.Start(ctx)

	// Non-review update: no push.
	hub.broadcast(Event{Type: "task.updated", Payload: model.Task{ID: "t1", Status: "running"}})
	time.Sleep(3 * n.debounce)
	if rt.count() != 0 {
		t.Fatalf("running task should not push, got %d", rt.count())
	}

	// Transition into review: push.
	hub.broadcast(Event{Type: "task.updated", Payload: model.Task{ID: "t1", Status: model.TaskReview}})
	waitCount(t, rt, 1)

	// Repeated review status (no transition): no second push.
	hub.broadcast(Event{Type: "task.updated", Payload: model.Task{ID: "t1", Status: model.TaskReview}})
	time.Sleep(3 * n.debounce)
	if rt.count() != 1 {
		t.Fatalf("re-broadcast of same status should not push, got %d", rt.count())
	}

	// Audit flag on merge: push.
	hub.broadcast(Event{Type: "task.updated", Payload: model.Task{ID: "t2", Status: model.TaskMerged, AuditFlagged: true}})
	waitCount(t, rt, 2)
}

func TestNotifierSkipsWhenPhoneAttached(t *testing.T) {
	n, m, hub, rt, _ := newTestNotifier(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.Start(ctx)

	m.active.Add(1)
	defer m.active.Add(-1)
	hub.broadcast(Event{Type: "decision.created", Payload: model.Decision{}})
	time.Sleep(3 * n.debounce)
	if rt.count() != 0 {
		t.Fatalf("attached phone should suppress push, got %d", rt.count())
	}
}

func TestNotifierPrunesGoneSubscriptions(t *testing.T) {
	n, _, hub, rt, daemonID := newTestNotifier(t)
	rt.status = http.StatusGone
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.Start(ctx)

	hub.broadcast(Event{Type: "decision.created", Payload: model.Decision{}})
	waitCount(t, rt, 1)
	waitFor(t, "sub pruned", func() bool {
		subs, _ := n.store.Relay.PushSubs(daemonID)
		return len(subs) == 0
	})
}
