package gateway

import (
	"log"
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

func (h *Hub) Run() {
	for {
		select {
		case op := <-h.register:
			if h.MaxWSClients > 0 && len(h.clients) >= h.MaxWSClients {
				op.ok <- false
				continue
			}
			h.clients[op.client] = true
			op.ok <- true
			log.Printf("gateway/hub: client %s registered. Total active: %d", op.client.ID, len(h.clients))
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
				log.Printf("gateway/hub: client %s unregistered. Total active: %d", client.ID, len(h.clients))
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.Send <- message:
				default:
					close(client.Send)
					delete(h.clients, client)
				}
			}
		}
	}
}
