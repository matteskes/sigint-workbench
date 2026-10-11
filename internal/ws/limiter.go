// Package ws — per-hub client admission cap (§14.3; A8, follow-up #8).
//
// The cap is enforced synchronously at the upgrade handler, BEFORE the
// WebSocket handshake: hub registration itself is asynchronous (the Run
// loop drains a channel), so counting via ClientCount() at upgrade time
// would let a burst of simultaneous upgrades all pass the check. The
// limiter's atomic accounting is exact under concurrency: either the
// handshake is admitted and one slot is held until the connection's
// read loop exits, or it is rejected with 503.
package ws

import "sync/atomic"

// ClientLimiter bounds the number of concurrent WebSocket clients. A
// max of 0 (or less) means unlimited — Acquire always succeeds.
type ClientLimiter struct {
	max int64
	cur atomic.Int64
}

// NewClientLimiter creates a limiter admitting at most max concurrent
// clients; max <= 0 disables the cap.
func NewClientLimiter(max int) *ClientLimiter {
	return &ClientLimiter{max: int64(max)}
}

// Acquire claims one client slot. It returns false when the hub is at
// capacity and the caller must reject the upgrade. Acquire/Release
// pairs must be 1:1 (each acquired slot is released exactly once).
func (l *ClientLimiter) Acquire() bool {
	if l.max <= 0 {
		return true
	}
	for {
		cur := l.cur.Load()
		if cur >= l.max {
			return false
		}
		if l.cur.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// Release returns one previously acquired client slot.
func (l *ClientLimiter) Release() {
	if l.max <= 0 {
		return
	}
	l.cur.Add(-1)
}

// Active reports the number of currently held slots.
func (l *ClientLimiter) Active() int {
	return int(l.cur.Load())
}

// Max reports the configured cap (0 = unlimited).
func (l *ClientLimiter) Max() int {
	return int(l.max)
}
