package gateway

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// registerOp is a registration handshake: ok receives true when the client
// is admitted to the hub, false when rejected (e.g. max WebSocket clients).
type registerOp struct {
	client *Client
	ok     chan bool
}

// Hub maintains the set of active clients and broadcasts messages to the clients.
type Hub struct {
	// MaxWSClients caps concurrent registrations; 0 = unlimited.
	MaxWSClients int

	// Logger is the structured logger used by the hub. Falls back to nop when nil.
	Logger *zap.Logger

	// OnBroadcast is called synchronously for every message that is broadcast
	// locally. Use it to forward messages to Redis pub/sub for cross-instance
	// propagation. Nil = no hook.
	OnBroadcast func([]byte)

	// Registered clients.
	clients map[*Client]bool

	// Inbound messages from the clients.
	broadcast chan []byte

	// Register requests from the clients (synchronous ack via registerOp.ok).
	register chan registerOp

	// Unregister requests from clients.
	unregister chan *Client
}

func NewHub(maxWebSocketClients int) *Hub {
	return &Hub{
		MaxWSClients: maxWebSocketClients,
		broadcast:    make(chan []byte),
		register:     make(chan registerOp),
		unregister:   make(chan *Client),
		clients:      make(map[*Client]bool),
	}
}

func (h *Hub) log() *zap.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return zap.NewNop()
}

// Broadcast sends a message to all connected local clients.
// If OnBroadcast is set, it is also invoked before local delivery.
func (h *Hub) Broadcast(msg []byte) {
	if h.OnBroadcast != nil {
		h.OnBroadcast(msg)
	}
	select {
	case h.broadcast <- msg:
	default:
		// Channel full — drop message rather than block caller.
	}
}

// Run processes hub events until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for client := range h.clients {
				close(client.Send)
			}
			return
		case op := <-h.register:
			if h.MaxWSClients > 0 && len(h.clients) >= h.MaxWSClients {
				op.ok <- false
				continue
			}
			h.clients[op.client] = true
			op.ok <- true
			h.log().Info("gateway/hub: client registered",
				zap.String("client_id", op.client.ID),
				zap.Int("total_active", len(h.clients)),
			)
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
				h.log().Info("gateway/hub: client unregistered",
					zap.String("client_id", client.ID),
					zap.Int("total_active", len(h.clients)),
				)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.Send <- message:
				case <-time.After(100 * time.Millisecond):
					// Client's send channel is back-pressured. Log and drop
					// this message rather than closing the connection.
					h.log().Warn("gateway/hub: client send back-pressured, dropping message",
						zap.String("client_id", client.ID),
					)
				}
			}
		}
	}
}
