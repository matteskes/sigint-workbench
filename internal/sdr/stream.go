// Package sdr — UDP IQ streamer (sender) and receiver.
package sdr

import (
	"fmt"
	"net"
	"sync"
)

// IQStreamer sends IQ frames to a target over UDP.
type IQStreamer struct {
	conn *net.UDPConn
	mu   sync.Mutex
}

// NewIQStreamer creates a UDP streamer targeting host:port.
func NewIQStreamer(host string, port int) (*IQStreamer, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, fmt.Errorf("sdr: resolve: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("sdr: dial: %w", err)
	}
	return &IQStreamer{conn: conn}, nil
}

// Send transmits one IQ frame.
func (s *IQStreamer) Send(f *IQFrame) error {
	data, err := f.Encode()
	if err != nil {
		return err
	}
	s.mu.Lock()
	_, err = s.conn.Write(data)
	s.mu.Unlock()
	return err
}

// Close closes the UDP connection.
func (s *IQStreamer) Close() error {
	return s.conn.Close()
}

// IQReceiver listens for IQ frames on a UDP port.
type IQReceiver struct {
	conn   *net.UDPConn
	ch     chan *IQFrame
	done   chan struct{}
	closed bool
}

// NewIQReceiver starts listening on the given port.
func NewIQReceiver(port int, bufSize int) (*IQReceiver, <-chan *IQFrame, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, nil, err
	}
	if bufSize <= 0 {
		bufSize = 256
	}
	ch := make(chan *IQFrame, bufSize)
	r := &IQReceiver{conn: conn, ch: ch, done: make(chan struct{})}
	go r.listen()
	return r, ch, nil
}

func (r *IQReceiver) listen() {
	buf := make([]byte, IQHeaderSize+MaxIQSamplesPerFrame*4)
	for {
		select {
		case <-r.done:
			return
		default:
		}
		n, _, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.done:
				return
			default:
				continue
			}
		}
		frame, err := DecodeIQFrame(buf[:n])
		if err != nil {
			continue
		}
		select {
		case r.ch <- frame:
		case <-r.done:
			return
		}
	}
}

// Close stops the receiver.
func (r *IQReceiver) Close() error {
	if !r.closed {
		r.closed = true
		close(r.done)
	}
	return r.conn.Close()
}