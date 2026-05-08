package githubapp

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ConnectedClient represents one active WebSocket connection from a
// client's OpsIntelligence instance to the relay hub.
type ConnectedClient struct {
	InstallationID int64
	AccountLogin   string
	conn           *websocket.Conn
	send           chan []byte
	done           chan struct{}
}

// EventEnvelope is the JSON message pushed to the client over the WebSocket.
type EventEnvelope struct {
	DeliveryID string          `json:"delivery_id"`
	Event      string          `json:"event"`
	Action     string          `json:"action,omitempty"`
	Repository string          `json:"repository,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	ReceivedAt time.Time       `json:"received_at"`
}

// Hub manages all active WebSocket connections from client OpsIntelligence
// instances. It is safe for concurrent use.
type Hub struct {
	mu      sync.RWMutex
	clients map[int64]*ConnectedClient // keyed by installation_id

	upgrader websocket.Upgrader
	log      *zap.Logger
}

// NewHub creates an empty Hub.
func NewHub(log *zap.Logger) *Hub {
	if log == nil {
		log = zap.NewNop()
	}
	return &Hub{
		clients: make(map[int64]*ConnectedClient),
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			CheckOrigin:      func(r *http.Request) bool { return true },
		},
		log: log,
	}
}

// Connected returns true if a live WebSocket connection exists for the
// given installation_id.
func (h *Hub) Connected(installationID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[installationID]
	return ok
}

// Push delivers an event envelope to the client connected for
// installationID. Returns false when no connection is active.
func (h *Hub) Push(installationID int64, env *EventEnvelope) bool {
	h.mu.RLock()
	c, ok := h.clients[installationID]
	h.mu.RUnlock()
	if !ok {
		return false
	}

	data, err := json.Marshal(env)
	if err != nil {
		return false
	}

	select {
	case c.send <- data:
		return true
	case <-time.After(5 * time.Second):
		h.log.Warn("githubapp hub: push timeout, dropping client",
			zap.Int64("installation_id", installationID))
		h.remove(installationID)
		return false
	}
}

// Upgrade upgrades r to a WebSocket, registers the client under
// installationID, and pumps messages until the connection closes.
// This call blocks until the client disconnects.
func (h *Hub) Upgrade(w http.ResponseWriter, r *http.Request, c *ConnectedClient) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Warn("githubapp hub: ws upgrade failed",
			zap.Int64("installation_id", c.InstallationID),
			zap.Error(err))
		return
	}
	c.conn = conn
	c.send = make(chan []byte, 64)
	c.done = make(chan struct{})

	h.mu.Lock()
	h.clients[c.InstallationID] = c
	h.mu.Unlock()

	h.log.Info("githubapp hub: client connected",
		zap.Int64("installation_id", c.InstallationID),
		zap.String("account", c.AccountLogin))

	go h.writePump(c)
	h.readPump(c) // blocks until disconnect
}

// writePump sends queued messages to the WebSocket.
func (h *Hub) writePump(c *ConnectedClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil) //nolint:errcheck
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

// readPump drains the read side (keeps the connection alive) and detects
// client disconnects.
func (h *Hub) readPump(c *ConnectedClient) {
	defer func() {
		h.remove(c.InstallationID)
		c.conn.Close()
		close(c.done)
	}()
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(90 * time.Second)) //nolint:errcheck
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.log.Info("githubapp hub: client disconnected",
					zap.Int64("installation_id", c.InstallationID),
					zap.Error(err))
			}
			return
		}
	}
}

func (h *Hub) remove(installationID int64) {
	h.mu.Lock()
	delete(h.clients, installationID)
	h.mu.Unlock()
	h.log.Info("githubapp hub: client removed", zap.Int64("installation_id", installationID))
}
