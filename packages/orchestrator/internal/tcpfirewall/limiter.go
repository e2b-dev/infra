package tcpfirewall

import (
	"sync/atomic"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// ConnectionLimiter tracks and limits per-sandbox TCP connections.
type ConnectionLimiter struct {
	connections *smap.Map[*atomic.Int64]
}

// NewConnectionLimiter creates a new ConnectionLimiter.
func NewConnectionLimiter() *ConnectionLimiter {
	return &ConnectionLimiter{
		connections: smap.New[*atomic.Int64](),
	}
}

func (l *ConnectionLimiter) getCounter(sandboxID string) *atomic.Int64 {
	return l.connections.Upsert(sandboxID, &atomic.Int64{}, func(exists bool, valueInMap, newValue *atomic.Int64) *atomic.Int64 {
		if exists {
			return valueInMap
		}

		return newValue
	})
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
	counter, ok := l.connections.Get(sandboxID)
	if !ok {
		return
	}

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
	l.connections.Remove(sandboxID)
}

// Count returns the current connection count for a sandbox.
func (l *ConnectionLimiter) Count(sandboxID string) int64 {
	if counter, ok := l.connections.Get(sandboxID); ok {
		return counter.Load()
	}

	return 0
}
