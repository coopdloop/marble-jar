// Package realtime runs the WebSocket gateway backing the live jar queue.
// Events arrive from Redis pub/sub (so every API replica sees every marble)
// and are fanned out to org-scoped browser connections.
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/marble-jar/marble-jar/services/core/internal/eventbus"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
	sendBuffer     = 256
)

// client is one browser socket, scoped to a single organization.
type client struct {
	hub   *Hub
	conn  *websocket.Conn
	send  chan []byte
	orgID string
	log   *slog.Logger
}

// Hub multiplexes broadcast events to connected clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*client]struct{} // orgID -> clients

	broadcaster eventbus.Broadcaster
	log         *slog.Logger
	upgrader    websocket.Upgrader
}

func NewHub(b eventbus.Broadcaster, allowedOrigins []string, log *slog.Logger) *Hub {
	origins := map[string]bool{}
	for _, o := range allowedOrigins {
		origins[o] = true
	}
	return &Hub{
		clients:     map[string]map[*client]struct{}{},
		broadcaster: b,
		log:         log,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // non-browser client (SDK)
				}
				return origins[origin] || origins["*"]
			},
		},
	}
}

// Run consumes the broadcast channel until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) error {
	events, cancel, err := h.broadcaster.Subscribe(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			h.dispatch(ev)
		}
	}
}

func (h *Hub) dispatch(ev eventbus.Event) {
	msg, err := json.Marshal(map[string]any{
		"type":       ev.Type,
		"timestamp":  ev.OccurredAt,
		"payload":    ev.Payload,
		"event_id":   ev.ID,
		"org_scoped": true,
	})
	if err != nil {
		h.log.Warn("marshal ws event", "error", err)
		return
	}

	h.mu.RLock()
	targets := make([]*client, 0, len(h.clients[ev.OrganizationID]))
	for c := range h.clients[ev.OrganizationID] {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.send <- msg:
		default:
			// Slow consumer: drop the connection rather than stalling fanout.
			h.log.Warn("ws client backpressure, closing", "org", c.orgID)
			h.remove(c)
		}
	}
}

// ServeWS upgrades an authenticated request into a live queue subscription.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, orgID string) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	c := &client{
		hub:   h,
		conn:  conn,
		send:  make(chan []byte, sendBuffer),
		orgID: orgID,
		log:   h.log,
	}

	h.mu.Lock()
	if h.clients[orgID] == nil {
		h.clients[orgID] = map[*client]struct{}{}
	}
	h.clients[orgID][c] = struct{}{}
	count := len(h.clients[orgID])
	h.mu.Unlock()

	h.log.Info("ws client connected", "org", orgID, "connections", count)

	hello, _ := json.Marshal(map[string]any{
		"type":      "connection.ready",
		"timestamp": time.Now().UTC(),
	})
	c.send <- hello

	go c.writePump()
	go c.readPump()
	return nil
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.clients[c.orgID]
	if !ok {
		return
	}
	if _, ok := set[c]; !ok {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.clients, c.orgID)
	}
	close(c.send)
}

// Connections reports live socket count (exposed on /metrics).
func (h *Hub) Connections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, set := range h.clients {
		n += len(set)
	}
	return n
}

func (c *client) readPump() {
	defer func() {
		c.hub.remove(c)
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		// The queue feed is server-push only; reads exist to detect closure
		// and to honor client-side ping frames.
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
