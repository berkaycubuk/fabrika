package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Event is a push message broadcast to all connected UI clients over the
// WebSocket. Type is a dotted name (e.g. "agent.created"); Payload is the entity.
type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Hub fans out events to every connected WebSocket client and to in-process
// subscribers (e.g. the relay manager forwarding events to paired phones). It
// is safe for concurrent use.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	subs    map[chan Event]struct{}
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

func newHub() *Hub {
	return &Hub{clients: map[*client]struct{}{}, subs: map[chan Event]struct{}{}}
}

// Broadcast marshals the event and queues it to all connected clients. Slow
// clients that can't keep up are dropped rather than blocking the hub.
func (h *Hub) Broadcast(e Event) {
	data, err := json.Marshal(e)
	if err != nil {
		log.Printf("event marshal: %v", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// Buffer full: drop the client; it will reconnect and refetch.
			close(c.send)
			delete(h.clients, c)
		}
	}
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			// Full buffer drops the event, never blocks the hub; subscribers
			// refetch state, same philosophy as WS clients.
		}
	}
}

// Subscribe registers an in-process event listener with the given buffer.
// Events overflowing the buffer are dropped. cancel unregisters and closes
// the channel.
func (h *Hub) Subscribe(buf int) (events <-chan Event, cancel func()) {
	ch := make(chan Event, buf)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
	}
	h.mu.Unlock()
}

// serveWS upgrades the connection and pumps queued events to the client.
func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Only accept upgrades whose Origin is the local machine. WebSockets
		// bypass CORS, so without this any page the user visits could open the
		// event stream and read live task/plan/decision data. Patterns cover the
		// loopback hosts the UI may be served from (localhost vs 127.0.0.1).
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "[::1]:*", "localhost", "127.0.0.1"},
	})
	if err != nil {
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 32)}
	h.add(c)
	defer func() {
		h.remove(c)
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	// Reader goroutine: detect disconnect / drain client messages.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	// Writer loop: ship queued events with a periodic ping to keep alive.
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-c.send:
			if !ok {
				return
			}
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, data)
			wcancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}
