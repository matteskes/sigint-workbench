// ws-hub — WebSocket hub for real-time signal event broadcasting.
//
// Pipeline services POST JSON events to /api/events; the hub
// broadcasts them to all WebSocket clients connected at /ws.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"

	"sigint-workbench/internal/ws"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     ws.OriginCheckFunc(ws.AllowedOriginsFromEnv()),
}

// defaultMaxClients is the WS_HUB_MAX_CLIENTS default: generous headroom
// over any realistic UI fleet (§14.3) while still bounding a flood of
// connection attempts (A8). "0" explicitly disables the cap.
const defaultMaxClients = 1024

// envLookup is indirected for tests.
var envLookup = os.LookupEnv

// MaxClientsFromEnv reads WS_HUB_MAX_CLIENTS (§14.3): unset/empty →
// defaultMaxClients, "0" → unlimited (explicit opt-out), negative or
// invalid → defaultMaxClients (fail-safe: a typo must not silently
// remove the flood guard).
func MaxClientsFromEnv() int {
	raw, ok := envLookup("WS_HUB_MAX_CLIENTS")
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultMaxClients
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		fmt.Fprintf(os.Stderr,
			"ws-hub: invalid WS_HUB_MAX_CLIENTS %q — using default %d\n",
			raw, defaultMaxClients)
		return defaultMaxClients
	}
	return n
}

// eventTypes is the set of event types the hub accepts.
var eventTypes = map[string]bool{
	"signal.new":     true,
	"signal.update":  true,
	"signal.removed": true,
	"signal.tdoa":    true, // §9.6/§14.2 TDOA fix/locus events
	"sdr.status":     true,
	"audio.level":    true,
	"track.update":   true,
	"spectrum.frame": true, // §18 spectrum/waterfall display feed
}

// handleWS upgrades an HTTP connection to a WebSocket client. The
// admission cap (§14.3, A8 follow-up) is checked synchronously before
// the handshake: at capacity the upgrade is refused with 503 and a
// D10 error body (the gateway's /ws relay maps the non-101 upstream
// to its documented 502 "ws-hub unreachable"). A nil gate disables
// the cap (tests).
func handleWS(hub *ws.Hub, gate *ws.ClientLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gate != nil && !gate.Acquire() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"websocket client limit reached"}`))
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			if gate != nil {
				gate.Release()
			}
			return
		}
		hub.Register(conn) // installs the keepalive contract (A12)
		// Read loop (client→server messages are unused). Its exit —
		// disconnect or pong silence past the read deadline — is what
		// unregisters the client and releases the capacity slot.
		go func() {
			defer hub.Unregister(conn)
			if gate != nil {
				defer gate.Release()
			}
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
	}
}

// handleIngest accepts JSON events from pipeline services and
// broadcasts them to connected WebSocket clients.
func handleIngest(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		var ev ws.Event
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := dec.Decode(&ev); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if !eventTypes[ev.Type] {
			http.Error(w, `{"error":"unknown event type"}`, http.StatusBadRequest)
			return
		}
		hub.Broadcast(ev)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"accepted"}`))
	}
}

func main() {
	port := flag.Int("port", 8081, "WebSocket port")
	flag.Parse()

	hub := ws.NewHub()
	gate := ws.NewClientLimiter(MaxClientsFromEnv())
	go hub.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", handleWS(hub, gate))
	mux.HandleFunc("/api/events", handleIngest(hub))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok","clients":` + fmt.Sprintf("%d", hub.ClientCount()) +
			`,"max_clients":` + fmt.Sprintf("%d", gate.Max()) + `}`))
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: mux,
	}

	fmt.Printf("ws-hub: listening on :%d\n", *port)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("ws-hub: shutting down")
		os.Exit(0)
	}()

	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "ws-hub: %v\n", err)
	}
}
