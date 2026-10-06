// Package ws — WebSocket event hub for real-time signal updates.
package ws

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event is a real-time message broadcast to WebSocket clients.
type Event struct {
	Type    string          `json:"type"` // "signal.new", "signal.update", "signal.removed", "sdr.status", "audio.level"
	Payload json.RawMessage `json:"payload"`
}

// Backpressure and keepalive contract (§14.3; STACK-AUDIT A1/A12). A
// slow client sheds its own queue and is evicted by its deadlines — it
// can never stall the hub's broadcast loop or the /api/events ingest
// path.
const (
	// sendQueueSize bounds each client's outbound queue (frames).
	sendQueueSize = 256

	// writeWait bounds every frame write (event or ping). A client
	// whose TCP backpressure exceeds it is dropped instead of delaying
	// everyone behind it.
	writeWait = 5 * time.Second

	// pingPeriod is the hub→client ping cadence; pongWait is the read
	// deadline each pong refreshes. A peer that stops answering pongs
	// times out on read and is unregistered (zombie clients).
	pingPeriod = 25 * time.Second
	pongWait   = 60 * time.Second
)

// client is one connected WebSocket with a bounded outbound queue and
// a dedicated writer goroutine (§14.3 lossy-by-design fan-out).
type client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub manages WebSocket client connections and broadcasts events.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*client]struct{}
	broadcast  chan Event
	register   chan *websocket.Conn
	unregister chan *websocket.Conn

	// Keepalive timing (defaults in NewHub; overridable in tests
	// before Run starts).
	pingPeriod time.Duration
	pongWait   time.Duration
	writeWait  time.Duration
}

// NewHub creates a new event hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*client]struct{}),
		broadcast:  make(chan Event, 256),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn, 256),
		pingPeriod: pingPeriod,
		pongWait:   pongWait,
		writeWait:  writeWait,
	}
}

// Run starts the hub's event loop. Call in a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case conn := <-h.register:
			c := &client{conn: conn, send: make(chan []byte, sendQueueSize)}
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()
			// Keepalive: the hub pings every pingPeriod; each pong
			// refreshes the read deadline the caller's read loop runs
			// under. A silent peer's reads time out and the caller
			// unregisters it.
			_ = conn.SetReadDeadline(time.Now().Add(h.pongWait))
			conn.SetPongHandler(func(string) error {
				return conn.SetReadDeadline(time.Now().Add(h.pongWait))
			})
			go c.writePump(h)

		case conn := <-h.unregister:
			h.mu.Lock()
			for c := range h.clients {
				if c.conn == conn {
					delete(h.clients, c)
					close(c.send) // the Run loop owns close; writer exits
					conn.Close()  // break the caller's read loop
				}
			}
			h.mu.Unlock()

		case event := <-h.broadcast:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			h.mu.RLock()
			for c := range h.clients {
				deliver(c, data)
			}
			h.mu.RUnlock()
		}
	}
}

// writePump serializes all writes for one client — broadcast frames
// and pings, each bounded by writeWait. A write error (including a
// stalled TCP peer outlasting the deadline) unregisters the client so
// one dead connection cannot hold hub resources forever.
func (c *client) writePump(h *Hub) {
	ticker := time.NewTicker(h.pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case pkt, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(h.writeWait))
			if !ok {
				// Hub closed the queue (unregistered): say goodbye.
				_ = c.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, pkt); err != nil {
				h.Unregister(c.conn)
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(h.writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				h.Unregister(c.conn)
				return
			}
		}
	}
}

// deliver pushes one frame to a client, shedding the oldest queued
// frame on overflow (§14.3: missed events are UI gaps the next event
// or the reconnect bootstrap heals). Never blocks the broadcast loop —
// same shed-oldest pattern as streamer.deliver (§10.4.3).
func deliver(c *client, pkt []byte) {
	for {
		select {
		case c.send <- pkt:
			return
		default:
		}
		select {
		case <-c.send: // shed the oldest, retry the new one
		default:
			return // raced empty again — never spin
		}
	}
}

// Broadcast sends an event to all connected clients. Non-blocking:
// the event is dropped when the hub's fan-out buffer is full.
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

// Register adds a client to the hub and installs the keepalive
// contract (read deadline + pong handler). The read loop remains the
// caller's: it unregisters on read error (disconnect or pong silence).
func (h *Hub) Register(conn *websocket.Conn) {
	h.register <- conn
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(conn *websocket.Conn) {
	h.unregister <- conn
}
