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
	"syscall"

	"github.com/gorilla/websocket"

	"sigint-workbench/internal/ws"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     ws.OriginCheckFunc(ws.AllowedOriginsFromEnv()),
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

// handleWS upgrades an HTTP connection to a WebSocket client.
func handleWS(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn) // installs the keepalive contract (A12)
		// Read loop (client→server messages are unused). Its exit —
		// disconnect or pong silence past the read deadline — is what
		// unregisters the client.
		go func() {
			defer hub.Unregister(conn)
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
	go hub.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", handleWS(hub))
	mux.HandleFunc("/api/events", handleIngest(hub))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok","clients":` + fmt.Sprintf("%d", hub.ClientCount()) + `}`))
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
