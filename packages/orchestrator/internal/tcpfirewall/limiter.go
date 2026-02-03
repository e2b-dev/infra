package tcpfirewall

import (
	"sync"
	"sync/atomic"
)

// ConnectionLimiter tracks and limits per-sandbox TCP connections.
type ConnectionLimiter struct {
	connections sync.Map // map[string]*atomic.Int64
}

// NewConnectionLimiter creates a new ConnectionLimiter.
func NewConnectionLimiter() *ConnectionLimiter {
	return &ConnectionLimiter{}
}

func (l *ConnectionLimiter) getCounter(sandboxID string) *atomic.Int64 {
	counter, _ := l.connections.LoadOrStore(sandboxID, &atomic.Int64{})

	return counter.(*atomic.Int64)
}

// TryAcquire attempts to acquire a connection slot for a sandbox.
// Returns (current count after increment, true) if successful, or (current count, false) if limit exceeded.
// If maxLimit is negative, no limit is enforced. If maxLimit is 0, all connections are blocked.
func (l *ConnectionLimiter) TryAcquire(sandboxID string, maxLimit int) (int64, bool) {
	counter := l.getCounter(sandboxID)
	for {
		current := counter.Load()
		if maxLimit >= 0 && current >= int64(maxLimit) {
			return current, false
		}
		if counter.CompareAndSwap(current, current+1) {
			return current + 1, true
		}
	}
}

// Release decrements the connection count for a sandbox.
func (l *ConnectionLimiter) Release(sandboxID string) {
	val, ok := l.connections.Load(sandboxID)
	if !ok {
		return
	}

	counter := val.(*atomic.Int64)
	for {
		current := counter.Load()
		if current <= 0 {
			return
		}
		if counter.CompareAndSwap(current, current-1) {
			return
		}
	}
}

// Remove removes a sandbox entry entirely. Call when sandbox is terminated.
func (l *ConnectionLimiter) Remove(sandboxID string) {
	l.connections.Delete(sandboxID)
}

// Count returns the current connection count for a sandbox.
func (l *ConnectionLimiter) Count(sandboxID string) int64 {
	if val, ok := l.connections.Load(sandboxID); ok {
		return val.(*atomic.Int64).Load()
	}

	return 0
}
