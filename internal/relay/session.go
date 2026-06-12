package relay

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
)

const (
	handshakeTimeout = 30 * time.Second
	sessionInboundQ  = 32
	rpcConcurrency   = 4

	// maxRPCResponse keeps sealed frames under the portal's 1 MiB cap.
	maxRPCResponse = 900 << 10
)

// eventPrefixes are the Hub event families forwarded to phones. They cover
// everything the attention feed can change on: decisions, plans, task status
// transitions, and bigtask progress.
var eventPrefixes = []string{"decision.", "plan.", "task.", "bigtask."}

func eventForwardable(typ string) bool {
	for _, p := range eventPrefixes {
		if strings.HasPrefix(typ, p) {
			return true
		}
	}
	return false
}

// clientMsg is a decrypted phone→daemon message.
type clientMsg struct {
	Kind     string          `json:"kind"` // rpc | push.sub | push.unsub
	ID       string          `json:"id,omitempty"`
	Method   string          `json:"method,omitempty"`
	Path     string          `json:"path,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
	Sub      *pushSub        `json:"sub,omitempty"`
	Endpoint string          `json:"endpoint,omitempty"`
}

// pushSub matches the browser PushSubscription.toJSON() shape.
type pushSub struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// serverMsg is a daemon→phone message.
type serverMsg struct {
	Kind      string          `json:"kind"` // rpc.res | event | vapid
	ID        string          `json:"id,omitempty"`
	Status    int             `json:"status,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
	Type      string          `json:"type,omitempty"`
	Payload   any             `json:"payload,omitempty"`
	PublicKey string          `json:"publicKey,omitempty"`
}

// session is one phone attached through the tunnel.
type session struct {
	id      string
	inbound chan []byte
	cancel  context.CancelFunc
}

func (m *Manager) startSession(ctx context.Context, t *tunnel, sid string, identity Keypair, daemonID string, reap chan<- string) *session {
	sctx, cancel := context.WithCancel(ctx)
	s := &session{id: sid, inbound: make(chan []byte, sessionInboundQ), cancel: cancel}
	go func() {
		defer cancel()
		m.runSession(sctx, t, s, identity, daemonID)
		select {
		case reap <- sid:
		case <-ctx.Done():
		}
	}()
	return s
}

// runSession performs the E2E handshake then serves RPCs, push subscription
// changes, and event forwarding until the session ends.
func (m *Manager) runSession(ctx context.Context, t *tunnel, s *session, identity Keypair, daemonID string) {
	closeSession := func(reason string) {
		_ = t.writeEnvelope(envelope{T: envClose, SID: s.id, Reason: reason})
	}

	// --- Handshake ---
	var msg1 []byte
	select {
	case msg1 = <-s.inbound:
	case <-time.After(handshakeTimeout):
		closeSession("handshake timeout")
		return
	case <-ctx.Done():
		return
	}
	msg2, sess, deviceID, err := Respond(rand.Reader, msg1, daemonID, identity.Priv, m.projectName, m.authorize(daemonID))
	if err != nil {
		closeSession("handshake failed")
		return
	}
	if err := t.writeEnvelope(envelope{T: envData, SID: s.id, Data: msg2}); err != nil {
		return
	}
	_ = m.store.Relay.TouchDevice(deviceID)
	m.logf("relay: phone session %s attached (device %s)", s.id, deviceID)

	m.active.Add(1)
	defer m.active.Add(-1)

	// All outbound frames serialize through one mutex: Session.Seal counters
	// must increase monotonically and the tunnel write order must match.
	var outMu sync.Mutex
	out := func(v serverMsg) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		outMu.Lock()
		defer outMu.Unlock()
		frame, err := sess.Seal(b)
		if err != nil {
			closeSession("session expired")
			return false
		}
		return t.writeEnvelope(envelope{T: envData, SID: s.id, Data: frame}) == nil
	}

	if key := m.vapidPublicKey(); key != "" {
		out(serverMsg{Kind: "vapid", PublicKey: key})
	}

	var events <-chan Event
	var cancelSub func()
	if m.subscribe != nil {
		events, cancelSub = m.subscribe(64)
		defer cancelSub()
	}

	sem := make(chan struct{}, rpcConcurrency)
	for {
		select {
		case <-ctx.Done():
			return

		case data := <-s.inbound:
			pt, err := sess.Open(data)
			if err != nil {
				closeSession("bad frame")
				return
			}
			var msg clientMsg
			if err := json.Unmarshal(pt, &msg); err != nil {
				closeSession("bad message")
				return
			}
			switch msg.Kind {
			case "rpc":
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				go func(msg clientMsg) {
					defer func() { <-sem }()
					status, body := dispatchRPC(m.handlerRef(), msg.Method, msg.Path, msg.Body)
					if len(body) > maxRPCResponse {
						status = 502
						body, _ = json.Marshal(map[string]string{"error": "response too large for relay"})
					}
					out(serverMsg{Kind: "rpc.res", ID: msg.ID, Status: status, Body: body})
				}(msg)
			case "push.sub":
				if msg.Sub != nil && msg.Sub.Endpoint != "" {
					_ = m.store.Relay.AddPushSub(&model.RelayPushSub{
						Endpoint: msg.Sub.Endpoint,
						DaemonID: daemonID,
						DeviceID: deviceID,
						P256DH:   msg.Sub.Keys.P256dh,
						Auth:     msg.Sub.Keys.Auth,
					})
				}
			case "push.unsub":
				if msg.Endpoint != "" {
					_ = m.store.Relay.DeletePushSub(msg.Endpoint)
				}
			}

		case e, ok := <-events:
			if !ok {
				return
			}
			if eventForwardable(e.Type) {
				out(serverMsg{Kind: "event", Type: e.Type, Payload: e.Payload})
			}
		}
	}
}
