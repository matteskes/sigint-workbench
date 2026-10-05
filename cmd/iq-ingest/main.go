// iq-ingest - Receives IQ streams over UDP and fans out to consumers.
//
// Listens on a UDP port for IQ frames from sdr-capture, then
// forwards each frame to a configurable list of consumer addresses
// (signal-processor, recorder, etc.).
//
// Run:
//
//	./bin/iq-ingest -port 9000 -consumers "signal-processor:9010"
//
// Or with env vars (Docker):
//
//	LISTEN_PORT=9000 CONSUMERS=signal-processor:9010 ./bin/iq-ingest
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"sigint-workbench/internal/config"
	"sigint-workbench/internal/sdr"
)

// consumer is a single fan-out target.
type consumer struct {
	name string
	conn *net.UDPConn
}

func newConsumer(addr string) (*consumer, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	return &consumer{name: addr, conn: conn}, nil
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// joinConsumerAddrs renders the YAML consumer list as a
// host:port,host:port CSV (the same shape as the CONSUMERS env).
func joinConsumerAddrs(cs []config.ConsumerConfig) string {
	addrs := make([]string, 0, len(cs))
	for _, c := range cs {
		if a := c.Addr(); a != "" {
			addrs = append(addrs, a)
		}
	}
	return strings.Join(addrs, ",")
}

func main() {
	configPath := flag.String("config", config.GetEnv("CONFIG", "config/iq-ingest.yaml"), "YAML config file")
	port := flag.Int("port", 0, "UDP listen port (overrides env/config)")
	consumersFlag := flag.String("consumers", "", "consumer list host:port,... (overrides env/config)")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("iq-ingest: ")

	// YAML is the source of truth (§16.1); env and flags override.
	var fileCfg config.IQIngestConfig
	if err := config.Load(*configPath, &fileCfg); err != nil {
		log.Printf("%v; using defaults/env", err)
	}
	listenPort := config.ResolveInt(*port, envInt("LISTEN_PORT", 0), fileCfg.ListenPort, 9000)
	consumersCSV := config.ResolveString(*consumersFlag, os.Getenv("CONSUMERS"), joinConsumerAddrs(fileCfg.Consumers))

	// Parse consumer list
	var consumers []*consumer
	if consumersCSV != "" {
		for _, part := range strings.Split(consumersCSV, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			c, err := newConsumer(part)
			if err != nil {
				log.Fatalf("consumer %q: %v", part, err)
			}
			consumers = append(consumers, c)
			log.Printf("consumer: %s", part)
		}
	}
	if len(consumers) == 0 {
		log.Println("WARNING: no consumers configured")
	}

	// Create UDP listener
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", listenPort))
	if err != nil {
		log.Fatalf("resolve: %v", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer conn.Close()
	log.Printf("listening on UDP :%d", listenPort)

	// Stats counters
	var packetsRecv, packetsSent, bytesRecv, dropped atomic.Int64
	seqs := sdr.NewSeqTracker() // §4.5/§9.6 per-sender gap accounting

	// Stats goroutine
	stopStats := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var lastPkt, lastSent int64
		for {
			select {
			case <-stopStats:
				return
			case <-ticker.C:
				pkt := packetsRecv.Load()
				sent := packetsSent.Load()
				drop := dropped.Load()
				rate := float64(pkt-lastPkt) / 5.0
				sRate := float64(sent-lastSent) / 5.0
				lastPkt, lastSent = pkt, sent
				if rate > 0 {
					log.Printf("stats: %.0f pkt/s in, %.0f pkt/s out, %d dropped", rate, sRate, drop)
				}
				seqs.LogGaps() // §4.5: gap/reorder report (silent when clean)
			}
		}
	}()

	// Buffer for max frame size
	buf := make([]byte, sdr.MaxIQDatagramSize) // v2 max datagram (§4.5)

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("shutting down (%v)", sig)
		close(stopStats)
		conn.Close()
		for _, c := range consumers {
			c.conn.Close()
		}
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()

	// Main receive loop
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-sigCh:
				return
			default:
				log.Printf("read: %v", err)
				time.Sleep(50 * time.Millisecond)
				continue
			}
		}
		packetsRecv.Add(1)
		bytesRecv.Add(int64(n))

		// Validate frame
		frame, err := sdr.DecodeIQFrame(buf[:n])
		if err != nil {
			dropped.Add(1)
			continue
		}
		// §4.5/§9.6: per-sender gap accounting from v2 seq (v1
		// frames carry no ordering information).
		if frame.V2 {
			seqs.Observe(frame.SDRID, frame.Seq)
		}

		// Fan out to all consumers
		for _, c := range consumers {
			if _, err := c.conn.Write(buf[:n]); err != nil {
				log.Printf("fan-out to %s: %v", c.name, err)
				continue
			}
			packetsSent.Add(1)
		}
	}
}
