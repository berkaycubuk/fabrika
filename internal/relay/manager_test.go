package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/berkaycubuk/fabrika/internal/store"
)

// fakePortal implements the portal's tunnel+connect envelope semantics in
// ~80 lines so fabrika's relay client is tested without importing the portal
// module (the wire format is the contract, not shared code).
type fakePortal struct {
	t  *testing.T
	mu sync.Mutex
	// single daemon, multiple client sessions
	daemonConn *websocket.Conn
	daemonID   string
	writeMu    sync.Mutex
	sessions   map[string]*websocket.Conn
	nextSID    int
}

func newFakePortal(t *testing.T) (*fakePortal, *httptest.Server) {
	fp := &fakePortal{t: t, sessions: map[string]*websocket.Conn{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/tunnel", fp.handleTunnel)
	mux.HandleFunc("GET /v1/connect/{daemon}", fp.handleConnect)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return fp, srv
}

func (fp *fakePortal) online() string {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if fp.daemonConn == nil {
		return ""
	}
	return fp.daemonID
}

func (fp *fakePortal) writeToDaemon(env envelope) {
	b, _ := json.Marshal(env)
	fp.writeMu.Lock()
	defer fp.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fp.mu.Lock()
	conn := fp.daemonConn
	fp.mu.Unlock()
	if conn != nil {
		_ = conn.Write(ctx, websocket.MessageText, b)
	}
}

func (fp *fakePortal) handleTunnel(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "no token", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	fp.mu.Lock()
	fp.daemonConn = conn
	fp.daemonID = r.Header.Get("X-Fabrika-Daemon")
	fp.mu.Unlock()
	defer func() {
		fp.mu.Lock()
		fp.daemonConn = nil
		fp.mu.Unlock()
	}()
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return
		}
		fp.mu.Lock()
		client := fp.sessions[env.SID]
		fp.mu.Unlock()
		if client == nil {
			continue
		}
		switch env.T {
		case envData:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			_ = client.Write(ctx, websocket.MessageBinary, env.Data)
			cancel()
		case envClose:
			client.Close(websocket.StatusNormalClosure, env.Reason)
			fp.mu.Lock()
			delete(fp.sessions, env.SID)
			fp.mu.Unlock()
		}
	}
}

func (fp *fakePortal) handleConnect(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	fp.mu.Lock()
	fp.nextSID++
	sid := fmt.Sprintf("sid-%d", fp.nextSID)
	fp.sessions[sid] = conn
	fp.mu.Unlock()
	fp.writeToDaemon(envelope{T: envOpen, SID: sid})
	defer func() {
		fp.mu.Lock()
		if _, live := fp.sessions[sid]; live {
			delete(fp.sessions, sid)
			fp.mu.Unlock()
			fp.writeToDaemon(envelope{T: envClose, SID: sid, Reason: "client disconnected"})
			return
		}
		fp.mu.Unlock()
	}()
	for {
		typ, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		fp.writeToDaemon(envelope{T: envData, SID: sid, Data: data})
	}
}

// --- test fixtures ---

type fakeHub struct {
	mu   sync.Mutex
	subs []chan Event
}

func (h *fakeHub) subscribe(buf int) (<-chan Event, func()) {
	ch := make(chan Event, buf)
	h.mu.Lock()
	h.subs = append(h.subs, ch)
	h.mu.Unlock()
	return ch, func() {}
}

func (h *fakeHub) broadcast(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func testHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/attention", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"reviews":[],"decisions":[{"id":"d1","question":"which db?"}]}`)
	})
	mux.HandleFunc("POST /api/decisions/{id}/answer", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Answer  string `json:"answer"`
			Promote bool   `json:"promote"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Answer == "" {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%q,"answer":%q}`, r.PathValue("id"), body.Answer)
	})
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"secret":"should never be reachable"}`)
	})
	return mux
}

func newTestManager(t *testing.T, portalURL string, hub *fakeHub) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "global"), filepath.Join(dir, "project"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	for k, v := range map[string]string{
		SettingEnabled: "on",
		SettingURL:     portalURL,
		SettingToken:   "frk_testtoken",
	} {
		if err := st.Settings.Set(k, v); err != nil {
			t.Fatalf("settings: %v", err)
		}
	}
	m := NewManager(Options{
		Store:       st,
		Subscribe:   hub.subscribe,
		ProjectRoot: "/repo/test",
		ProjectName: "testproj",
		Logf:        t.Logf,
	})
	m.SetHandler(testHandler())
	return m, st
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// testPhone drives the phone side: WebSocket to the portal + Noise initiator.
type testPhone struct {
	conn *websocket.Conn
	sess *Session
	m2   Msg2Payload
}

func dialPhone(t *testing.T, portalURL, daemonID string, daemonPub [32]byte, static Keypair, payload Msg1Payload) (*testPhone, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(portalURL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL+"/v1/connect/"+daemonID, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })

	msg1, st, err := Initiate(nil, daemonID, daemonPub, static, payload)
	if err == nil {
		err = conn.Write(ctx, websocket.MessageBinary, msg1)
	}
	if err != nil {
		return nil, err
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	_, msg2, err := conn.Read(rctx)
	if err != nil {
		return nil, err
	}
	sess, m2, err := st.Finish(msg2)
	if err != nil {
		return nil, err
	}
	return &testPhone{conn: conn, sess: sess, m2: m2}, nil
}

func (p *testPhone) send(t *testing.T, msg clientMsg) {
	t.Helper()
	b, _ := json.Marshal(msg)
	frame, err := p.sess.Seal(b)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatalf("phone write: %v", err)
	}
}

func (p *testPhone) read(t *testing.T) serverMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, frame, err := p.conn.Read(ctx)
	if err != nil {
		t.Fatalf("phone read: %v", err)
	}
	pt, err := p.sess.Open(frame)
	if err != nil {
		t.Fatalf("phone Open: %v", err)
	}
	var msg serverMsg
	if err := json.Unmarshal(pt, &msg); err != nil {
		t.Fatalf("phone unmarshal: %v", err)
	}
	return msg
}

// rpc sends an RPC and reads until its response arrives (events may interleave).
func (p *testPhone) rpc(t *testing.T, id, method, path string, body any) serverMsg {
	t.Helper()
	var raw json.RawMessage
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	p.send(t, clientMsg{Kind: "rpc", ID: id, Method: method, Path: path, Body: raw})
	for i := 0; i < 16; i++ {
		msg := p.read(t)
		if msg.Kind == "rpc.res" && msg.ID == id {
			return msg
		}
	}
	t.Fatalf("rpc %s: no response", id)
	return serverMsg{}
}

func TestRelayEndToEnd(t *testing.T) {
	fp, portal := newFakePortal(t)
	hub := &fakeHub{}
	m, st := newTestManager(t, portal.URL, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, "daemon tunnel", func() bool { return fp.online() != "" })

	identity, daemonID, err := m.ensureIdentity()
	if err != nil {
		t.Fatalf("ensureIdentity: %v", err)
	}
	if fp.online() != daemonID {
		t.Fatalf("portal sees daemon %q, want %q", fp.online(), daemonID)
	}

	// --- Pair a new phone with the one-time secret ---
	secret, _, err := m.pair.New()
	if err != nil {
		t.Fatalf("pairing secret: %v", err)
	}
	phoneKey, _ := GenerateKeypair(nil)
	phone, err := dialPhone(t, portal.URL, daemonID, identity.Pub, phoneKey,
		Msg1Payload{V: 1, Secret: secret, Name: "integration phone"})
	if err != nil {
		t.Fatalf("dialPhone: %v", err)
	}
	if !phone.m2.Paired || phone.m2.Project != "testproj" {
		t.Fatalf("unexpected msg2: %+v", phone.m2)
	}
	devices, _ := st.Relay.Devices(daemonID)
	if len(devices) != 1 || devices[0].Name != "integration phone" {
		t.Fatalf("device not persisted: %+v", devices)
	}

	// --- RPC through the bridge ---
	res := phone.rpc(t, "r1", "GET", "/api/attention", nil)
	if res.Status != 200 || !strings.Contains(string(res.Body), "which db?") {
		t.Fatalf("attention rpc: %+v", res)
	}
	res = phone.rpc(t, "r2", "POST", "/api/decisions/d1/answer", map[string]any{"answer": "sqlite", "promote": false})
	if res.Status != 200 || !strings.Contains(string(res.Body), "sqlite") {
		t.Fatalf("answer rpc: %+v", res)
	}

	// --- Allowlist enforced even though the handler would serve it ---
	res = phone.rpc(t, "r3", "GET", "/api/settings", nil)
	if res.Status != http.StatusForbidden {
		t.Fatalf("settings should be 403 over relay, got %d %s", res.Status, res.Body)
	}

	// --- Event forwarding ---
	waitFor(t, "session subscription", func() bool { return m.HasActiveSessions() })
	hub.broadcast(Event{Type: "decision.created", Payload: map[string]string{"id": "d2"}})
	hub.broadcast(Event{Type: "agent.updated", Payload: "filtered out"})
	ev := phone.read(t)
	if ev.Kind != "event" || ev.Type != "decision.created" {
		t.Fatalf("expected decision.created event, got %+v", ev)
	}

	// --- Push subscription registration ---
	sub := clientMsg{Kind: "push.sub", Sub: &pushSub{}}
	sub.Sub.Endpoint = "https://push.example/ep1"
	sub.Sub.Keys.P256dh = "k"
	sub.Sub.Keys.Auth = "a"
	phone.send(t, sub)
	waitFor(t, "push sub persisted", func() bool {
		subs, _ := st.Relay.PushSubs(daemonID)
		return len(subs) == 1 && subs[0].Endpoint == "https://push.example/ep1"
	})

	// --- Known device reconnects without a secret ---
	phone2, err := dialPhone(t, portal.URL, daemonID, identity.Pub, phoneKey, Msg1Payload{V: 1})
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if phone2.m2.Paired {
		t.Fatal("reconnect should not re-pair")
	}

	// --- Unknown device without a secret is rejected ---
	strangerKey, _ := GenerateKeypair(nil)
	if _, err := dialPhone(t, portal.URL, daemonID, identity.Pub, strangerKey, Msg1Payload{V: 1}); err == nil {
		t.Fatal("stranger without secret should be rejected")
	}

	// --- Secret is single use ---
	if _, err := dialPhone(t, portal.URL, daemonID, identity.Pub, strangerKey,
		Msg1Payload{V: 1, Secret: secret, Name: "replay"}); err == nil {
		t.Fatal("pairing secret must be single use")
	}
}

func TestManagerDisabled(t *testing.T) {
	hub := &fakeHub{}
	m, st := newTestManager(t, "http://127.0.0.1:1", hub) // never dialed
	if err := st.Settings.Set(SettingEnabled, "off"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	if s := m.Status(); s.Enabled || s.Connected {
		t.Fatalf("disabled relay should stay idle: %+v", s)
	}
}
