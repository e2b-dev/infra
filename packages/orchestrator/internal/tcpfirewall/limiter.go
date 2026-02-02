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

// getCounter returns the atomic counter for a sandbox, creating one if needed.
func (l *ConnectionLimiter) getCounter(sandboxID string) *atomic.Int64 {
	counter, _ := l.connections.LoadOrStore(sandboxID, &atomic.Int64{})

	return counter.(*atomic.Int64)
}

// TryAcquire attempts to acquire a connection slot for a sandbox.
// Returns (current count after increment, true) if successful, or (current count, false) if limit exceeded.
// If maxLimit is 0 or negative, no limit is enforced.
func (l *ConnectionLimiter) TryAcquire(sandboxID string, maxLimit int) (int64, bool) {
	counter := l.getCounter(sandboxID)

	if maxLimit <= 0 {
		newCount := counter.Add(1)

		return newCount, true
	}

	for {
		current := counter.Load()
		if current >= int64(maxLimit) {
			return current, false
		}

		if counter.CompareAndSwap(current, current+1) {
			return current + 1, true
		}
	}
}

// Release releases a connection slot for a sandbox.
// If the count drops to 0, the entry is removed from the map to prevent memory leaks.
func (l *ConnectionLimiter) Release(sandboxID string) {
	counter := l.getCounter(sandboxID)
	newCount := counter.Add(-1)

	if newCount <= 0 {
		l.connections.Delete(sandboxID)
	}
}

// Count returns the current connection count for a sandbox.
func (l *ConnectionLimiter) Count(sandboxID string) int64 {
	counter := l.getCounter(sandboxID)

	return counter.Load()
}
