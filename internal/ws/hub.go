// Package ws — WebSocket event hub for real-time signal updates.
package ws

import (
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
)

// Event is a real-time message broadcast to WebSocket clients.
type Event struct {
	Type    string          `json:"type"` // "signal.new", "signal.update", "signal.removed", "sdr.status", "audio.level"
	Payload json.RawMessage `json:"payload"`
}

// Hub manages WebSocket client connections and broadcasts events.
type Hub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
	broadcast chan Event
	register  chan *websocket.Conn
	unregister chan *websocket.Conn
}

// NewHub creates a new event hub.
func NewHub() *Hub {
	return &Hub{
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan Event, 256),
		register:  make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn, 256),
	}
}

// Run starts the hub's event loop. Call in a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if h.clients[client] {
				delete(h.clients, client)
				client.Close()
			}
			h.mu.Unlock()

		case event := <-h.broadcast:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			h.mu.RLock()
			for client := range h.clients {
				if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
					h.mu.RUnlock()
					h.mu.Lock()
					delete(h.clients, client)
					client.Close()
					h.mu.Unlock()
					h.mu.RLock()
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends an event to all connected clients.
func (h *Hub) Broadcast(e Event) {
	select {
	case h.broadcast <- e:
	default:
		// Drop if buffer is full (non-blocking)
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Register adds a client to the hub.
func (h *Hub) Register(conn *websocket.Conn) {
	h.register <- conn
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(conn *websocket.Conn) {
	h.unregister <- conn
}