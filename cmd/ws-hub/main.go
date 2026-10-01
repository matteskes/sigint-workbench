// ws-hub — WebSocket hub for real-time signal event broadcasting.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"sigint-workbench/internal/ws"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func main() {
	port := flag.Int("port", 8081, "WebSocket port")
	flag.Parse()

	hub := ws.NewHub()
	go hub.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn)
		// Read loop (discard client messages for now)
		go func() {
			defer hub.Unregister(conn)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
	})
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
		ctx := srv.Context()
		_ = ctx
		fmt.Println("ws-hub: shutting down")
		os.Exit(0)
	}()

	_ = time.Second
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "ws-hub: %v\n", err)
	}
}