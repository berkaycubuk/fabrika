package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log"
	mrand "math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// Settings keys (global store) that configure relay mode.
const (
	SettingEnabled      = "relay_enabled"       // "on" | anything else = off
	SettingURL          = "relay_url"           // portal base URL (https://relay.example.com)
	SettingToken        = "relay_token"         // portal relay token (frk_…)
	SettingVAPIDPublic  = "relay_vapid_public"  // Web Push VAPID keypair, minted on first use
	SettingVAPIDPrivate = "relay_vapid_private" // (see notify.go)
)

// DefaultURL is the hosted portal a fresh instance points at until the user
// overrides it.
const DefaultURL = "https://relay.fabrika-ai.com"

// Event mirrors api.Event without importing internal/api (api constructs the
// Manager, so relay must not import api back).
type Event struct {
	Type    string
	Payload any
}

// Subscriber taps the server's event hub; see api.Hub.Subscribe.
type Subscriber func(buf int) (events <-chan Event, cancel func())

// Options wires a Manager into the daemon.
type Options struct {
	Store       *store.Store
	Subscribe   Subscriber
	ProjectRoot string
	ProjectName string
	Logf        func(format string, args ...any) // defaults to log.Printf
}

// Status is the relay state surfaced to the local UI.
type Status struct {
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	DaemonID  string `json:"daemonId"`
	Sessions  int    `json:"sessions"`
	LastError string `json:"lastError"`
}

// Manager owns the daemon side of relay mode: the outbound tunnel with
// reconnect, the E2E sessions multiplexed on it, pairing, and (via notify.go)
// push notifications. Construct with NewManager, hand it the API handler with
// SetHandler, then Start it alongside the engine.
type Manager struct {
	store       *store.Store
	subscribe   Subscriber
	projectRoot string
	projectName string
	logf        func(format string, args ...any)

	pair   pairings
	active atomic.Int32 // established phone sessions

	mu         sync.Mutex
	handler    http.Handler
	baseCtx    context.Context
	loopCancel context.CancelFunc
	connected  bool
	lastErr    string
	identity   *Keypair
	daemonID   string
}

func NewManager(o Options) *Manager {
	logf := o.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &Manager{
		store:       o.Store,
		subscribe:   o.Subscribe,
		projectRoot: o.ProjectRoot,
		projectName: o.ProjectName,
		logf:        logf,
	}
}

// SetHandler injects the API mux the RPC bridge replays requests into. Called
// once by api.Server.Handler() (the handler doesn't exist yet at NewServer
// time).
func (m *Manager) SetHandler(h http.Handler) {
	m.mu.Lock()
	m.handler = h
	m.mu.Unlock()
}

func (m *Manager) handlerRef() http.Handler {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handler
}

// Start records the lifecycle context and connects if relay is enabled.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.baseCtx = ctx
	m.mu.Unlock()
	m.Reload()
}

// Reload re-reads settings and restarts (or stops) the tunnel loop. Called
// after the relay settings change.
func (m *Manager) Reload() {
	m.mu.Lock()
	if m.loopCancel != nil {
		m.loopCancel()
		m.loopCancel = nil
	}
	base := m.baseCtx
	m.mu.Unlock()
	if base == nil || base.Err() != nil {
		return
	}

	enabled, url, token, err := m.settings()
	if err != nil {
		m.setStatus(false, "read settings: "+err.Error())
		return
	}
	if !enabled || url == "" || token == "" {
		m.setStatus(false, "")
		return
	}

	if _, _, err := m.ensureIdentity(); err != nil {
		m.setStatus(false, "identity: "+err.Error())
		return
	}

	ctx, cancel := context.WithCancel(base)
	m.mu.Lock()
	m.loopCancel = cancel
	m.mu.Unlock()
	go m.run(ctx, url, token)
}

// Status reports relay state for the local UI.
func (m *Manager) Status() Status {
	enabled, _, _, _ := m.settings()
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{
		Enabled:   enabled,
		Connected: m.connected,
		DaemonID:  m.daemonID,
		Sessions:  int(m.active.Load()),
		LastError: m.lastErr,
	}
}

// HasActiveSessions reports whether a phone is currently attached (used to
// suppress push notifications the user would find redundant).
func (m *Manager) HasActiveSessions() bool { return m.active.Load() > 0 }

func (m *Manager) settings() (enabled bool, url, token string, err error) {
	if m.store == nil {
		return false, "", "", errors.New("no store")
	}
	en, err := m.store.Settings.Get(SettingEnabled)
	if err != nil {
		return false, "", "", err
	}
	url, err = m.store.Settings.Get(SettingURL)
	if err != nil {
		return false, "", "", err
	}
	token, err = m.store.Settings.Get(SettingToken)
	if err != nil {
		return false, "", "", err
	}
	return en == "on", url, token, nil
}

func (m *Manager) setStatus(connected bool, lastErr string) {
	m.mu.Lock()
	m.connected = connected
	m.lastErr = lastErr
	m.mu.Unlock()
}

// ensureIdentity loads (or mints) the per-project static keypair and derives
// the daemon id from it.
func (m *Manager) ensureIdentity() (Keypair, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.identity != nil {
		return *m.identity, m.daemonID, nil
	}
	priv, err := m.store.Relay.Identity(m.projectRoot)
	if err != nil {
		return Keypair{}, "", err
	}
	var kp Keypair
	if priv == nil {
		kp, err = GenerateKeypair(rand.Reader)
		if err != nil {
			return Keypair{}, "", err
		}
		if err := m.store.Relay.SaveIdentity(m.projectRoot, kp.Priv[:]); err != nil {
			return Keypair{}, "", err
		}
	} else {
		copy(kp.Priv[:], priv)
		pub, err := PublicKey(kp.Priv[:])
		if err != nil {
			return Keypair{}, "", err
		}
		copy(kp.Pub[:], pub)
	}
	m.identity = &kp
	m.daemonID = DaemonID(kp.Pub[:])
	return kp, m.daemonID, nil
}

// run dials the portal and serves the tunnel, reconnecting with exponential
// backoff until ctx is cancelled.
func (m *Manager) run(ctx context.Context, url, token string) {
	identity, daemonID, err := m.ensureIdentity()
	if err != nil {
		m.setStatus(false, err.Error())
		return
	}

	const maxBackoff = time.Minute
	backoff := time.Second
	for ctx.Err() == nil {
		t, status, err := dialTunnel(ctx, url, token, daemonID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.setStatus(false, "dial relay: "+err.Error())
			wait := backoff
			switch status {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict:
				// Token/account problem; reconnecting faster won't help.
				wait = maxBackoff
				m.setStatus(false, "relay rejected credentials (check token)")
			default:
				backoff = min(backoff*2, maxBackoff)
			}
			if !sleep(ctx, jitter(wait)) {
				return
			}
			continue
		}

		m.logf("relay: connected to %s as %s…", url, daemonID[:12])
		m.setStatus(true, "")
		start := time.Now()
		m.serveTunnel(ctx, t, identity, daemonID)
		t.close("daemon shutting down")
		m.setStatus(false, "")
		m.logf("relay: disconnected from %s", url)

		if time.Since(start) >= time.Minute {
			backoff = time.Second // stable connection; reset
		} else {
			backoff = min(backoff*2, maxBackoff)
		}
		if !sleep(ctx, jitter(backoff)) {
			return
		}
	}
}

// serveTunnel demuxes envelopes into per-session goroutines until the
// connection drops.
func (m *Manager) serveTunnel(ctx context.Context, t *tunnel, identity Keypair, daemonID string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	envCh := make(chan envelope)
	errCh := make(chan error, 1)
	go func() {
		for {
			env, err := t.readEnvelope(ctx)
			if err != nil {
				errCh <- err
				return
			}
			select {
			case envCh <- env:
			case <-ctx.Done():
				return
			}
		}
	}()

	reap := make(chan string, 16)
	sessions := map[string]*session{}
	defer func() {
		for _, s := range sessions {
			s.cancel()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-errCh:
			return
		case sid := <-reap:
			if s := sessions[sid]; s != nil {
				s.cancel()
				delete(sessions, sid)
			}
		case env := <-envCh:
			switch env.T {
			case envOpen:
				sessions[env.SID] = m.startSession(ctx, t, env.SID, identity, daemonID, reap)
			case envData:
				s := sessions[env.SID]
				if s == nil {
					continue
				}
				select {
				case s.inbound <- env.Data:
				default:
					// Session wedged; drop it rather than stall the tunnel.
					s.cancel()
					delete(sessions, env.SID)
					_ = t.writeEnvelope(envelope{T: envClose, SID: env.SID, Reason: "session overloaded"})
				}
			case envClose:
				if s := sessions[env.SID]; s != nil {
					s.cancel()
					delete(sessions, env.SID)
				}
			}
		}
	}
}

// authorize implements the handshake policy: known paired devices connect
// directly; an unknown device must present the live one-time pairing secret,
// which pairs it persistently.
func (m *Manager) authorize(daemonID string) Authorizer {
	return func(pub [32]byte, p Msg1Payload) (string, bool, bool) {
		devices, err := m.store.Relay.Devices(daemonID)
		if err != nil {
			return "", false, false
		}
		for _, d := range devices {
			if bytes.Equal(d.PubKey, pub[:]) {
				return d.ID, false, true
			}
		}
		if len(p.Secret) > 0 && m.pair.Consume(p.Secret) {
			d := &model.RelayDevice{DaemonID: daemonID, PubKey: pub[:], Name: p.Name}
			if err := m.store.Relay.AddDevice(d); err != nil {
				return "", false, false
			}
			m.logf("relay: paired new device %q (%s)", d.Name, d.ID)
			return d.ID, true, true
		}
		return "", false, false
	}
}

// vapidPublicKey returns the daemon's VAPID public key (set by the notifier,
// see notify.go); empty until notifications are configured.
func (m *Manager) vapidPublicKey() string {
	if m.store == nil {
		return ""
	}
	key, _ := m.store.Settings.Get(SettingVAPIDPublic)
	return key
}

func jitter(d time.Duration) time.Duration {
	return d + time.Duration(mrand.Int64N(int64(d)/5+1)) - d/10
}

// sleep waits d or until ctx is done; reports whether the wait completed.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
