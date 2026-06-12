package relay

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// debounceWindow batches attention triggers: at most one push per window so a
// plan fanning out into ten review-ready tasks doesn't buzz ten times.
const debounceWindow = 30 * time.Second

// Notifier watches the event stream for moments Fabrika needs a human —
// new decisions, plans awaiting approval, tasks entering review, audit
// flags — and sends a generic Web Push straight to each paired phone's push
// service. Content never says more than "needs attention": push payloads
// leave the machine, the details don't.
type Notifier struct {
	store     *store.Store
	mgr       *Manager
	subscribe Subscriber
	logf      func(format string, args ...any)

	// client posts to push services; tests swap the transport.
	client *http.Client
	// debounce is debounceWindow in production; tests shrink it.
	debounce time.Duration

	mu         sync.Mutex
	lastStatus map[string]string // task id → last seen status
	lastAudit  map[string]bool   // task id → last seen audit flag
	pending    bool              // a trigger is waiting on the debounce timer
}

// NewNotifier wires the notifier; Start it alongside the Manager.
func NewNotifier(s *store.Store, mgr *Manager, subscribe Subscriber) *Notifier {
	return &Notifier{
		store:      s,
		mgr:        mgr,
		subscribe:  subscribe,
		logf:       log.Printf,
		client:     &http.Client{Timeout: 15 * time.Second},
		debounce:   debounceWindow,
		lastStatus: map[string]string{},
		lastAudit:  map[string]bool{},
	}
}

// Start consumes events until ctx is cancelled.
func (n *Notifier) Start(ctx context.Context) {
	if n.subscribe == nil {
		return
	}
	// Mint the VAPID keypair up front: sessions send the public key to the
	// phone right after the handshake, before any push is ever attempted.
	if _, _, err := n.vapidKeys(); err != nil {
		n.logf("relay: vapid keys: %v", err)
	}
	events, cancel := n.subscribe(128)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-events:
				if !ok {
					return
				}
				if n.triggers(e) {
					n.schedule(ctx)
				}
			}
		}
	}()
}

// triggers reports whether an event means a human is newly needed.
func (n *Notifier) triggers(e Event) bool {
	switch e.Type {
	case "decision.created", "plan.ready":
		return true
	case "task.updated":
		t, ok := e.Payload.(model.Task)
		if !ok {
			if tp, ok2 := e.Payload.(*model.Task); ok2 && tp != nil {
				t, ok = *tp, true
			}
		}
		if !ok {
			return false
		}
		n.mu.Lock()
		defer n.mu.Unlock()
		prevStatus, prevAudit := n.lastStatus[t.ID], n.lastAudit[t.ID]
		n.lastStatus[t.ID], n.lastAudit[t.ID] = t.Status, t.AuditFlagged
		// Only transitions fire: a task entering review, or an auto-merge
		// getting flagged for audit. (Cold start after a daemon restart may
		// re-fire once for an already-review task; acceptable.)
		if t.Status == model.TaskReview && prevStatus != model.TaskReview {
			return true
		}
		if t.AuditFlagged && t.Status == model.TaskMerged && !prevAudit {
			return true
		}
	}
	return false
}

// schedule arms the debounce timer; only the first trigger in a window arms it.
func (n *Notifier) schedule(ctx context.Context) {
	n.mu.Lock()
	if n.pending {
		n.mu.Unlock()
		return
	}
	n.pending = true
	n.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(n.debounce):
		}
		n.mu.Lock()
		n.pending = false
		n.mu.Unlock()
		n.send(ctx)
	}()
}

// send pushes the generic notification to every subscription, unless a phone
// is already attached (the user is looking; buzzing is redundant).
func (n *Notifier) send(ctx context.Context) {
	if n.mgr.HasActiveSessions() {
		return
	}
	st := n.mgr.Status()
	if st.DaemonID == "" {
		return
	}
	subs, err := n.store.Relay.PushSubs(st.DaemonID)
	if err != nil || len(subs) == 0 {
		return
	}
	pub, priv, err := n.vapidKeys()
	if err != nil {
		n.logf("relay: vapid keys: %v", err)
		return
	}
	payload, _ := json.Marshal(map[string]string{
		"title": "Fabrika",
		"body":  "Needs your attention",
	})
	for _, sub := range subs {
		s := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256DH, Auth: sub.Auth},
		}
		resp, err := webpush.SendNotificationWithContext(ctx, payload, s, &webpush.Options{
			HTTPClient:      n.client,
			VAPIDPublicKey:  pub,
			VAPIDPrivateKey: priv,
			Subscriber:      "https://github.com/berkaycubuk/fabrika",
			TTL:             3600,
			Urgency:         webpush.UrgencyHigh,
		})
		if err != nil {
			n.logf("relay: push to %s: %v", sub.Endpoint, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			// Subscription expired or revoked; stop trying.
			_ = n.store.Relay.DeletePushSub(sub.Endpoint)
		}
	}
}

// vapidKeys returns the daemon's VAPID keypair, minting and persisting one on
// first use.
func (n *Notifier) vapidKeys() (pub, priv string, err error) {
	pub, err = n.store.Settings.Get(SettingVAPIDPublic)
	if err != nil {
		return "", "", err
	}
	priv, err = n.store.Settings.Get(SettingVAPIDPrivate)
	if err != nil {
		return "", "", err
	}
	if pub != "" && priv != "" {
		return pub, priv, nil
	}
	priv, pub, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", err
	}
	if err := n.store.Settings.Set(SettingVAPIDPublic, pub); err != nil {
		return "", "", err
	}
	if err := n.store.Settings.Set(SettingVAPIDPrivate, priv); err != nil {
		return "", "", err
	}
	return pub, priv, nil
}
