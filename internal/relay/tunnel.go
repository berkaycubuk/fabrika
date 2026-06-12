package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// envelope is the relay-level framing on the daemon tunnel. It mirrors the
// portal's wire format (the two repos share no module; this struct and the
// vectors in testdata/ are the contract).
type envelope struct {
	T      string `json:"t"`                // open | data | close
	SID    string `json:"sid,omitempty"`    // session id
	Data   []byte `json:"data,omitempty"`   // opaque E2E payload (base64 in JSON)
	Reason string `json:"reason,omitempty"` // optional close reason
}

const (
	envOpen  = "open"
	envData  = "data"
	envClose = "close"
)

const (
	tunnelWriteTimeout = 10 * time.Second
	maxEnvelopeBytes   = 2 << 20 // payload cap is 1 MiB; base64+JSON inflate ~4/3
)

// tunnel is one live connection to the portal. Writes are serialized so
// concurrent sessions can share it.
type tunnel struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

// dialTunnel opens the outbound WebSocket to the portal's /v1/tunnel.
// portalURL accepts http(s):// or ws(s):// forms. Returns the HTTP status of
// a rejected dial (0 when unknown) so callers can park on auth errors.
func dialTunnel(ctx context.Context, portalURL, token, daemonID string) (*tunnel, int, error) {
	u := strings.TrimRight(portalURL, "/")
	switch {
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dctx, u+"/v1/tunnel", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":    {"Bearer " + token},
			"X-Fabrika-Daemon": {daemonID},
		},
	})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		return nil, status, err
	}
	conn.SetReadLimit(maxEnvelopeBytes)
	return &tunnel{conn: conn}, 0, nil
}

func (t *tunnel) writeEnvelope(env envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), tunnelWriteTimeout)
	defer cancel()
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.conn.Write(ctx, websocket.MessageText, b)
}

// readEnvelope blocks for the next envelope from the portal.
func (t *tunnel) readEnvelope(ctx context.Context) (envelope, error) {
	for {
		typ, data, err := t.conn.Read(ctx)
		if err != nil {
			return envelope{}, err
		}
		if typ != websocket.MessageText {
			continue
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return envelope{}, err
		}
		return env, nil
	}
}

func (t *tunnel) close(reason string) {
	t.conn.Close(websocket.StatusNormalClosure, reason)
}
