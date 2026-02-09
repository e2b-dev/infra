package proxy

import (
	"sync"
	"time"
)

const notResumableNegativeTTL = 30 * time.Second

// negativeResumeCache keeps a short-lived in-memory record of sandboxes that are
// known to be non-resumable (based on API gRPC returning NotFound).
//
// This reduces repeated resume RPCs (and downstream DB load) for traffic that
// hits a paused/non-resumable sandbox at high RPS.
type negativeResumeCache struct {
	mu  sync.Mutex
	exp map[string]time.Time
}

func newNegativeResumeCache() *negativeResumeCache {
	return &negativeResumeCache{exp: map[string]time.Time{}}
}

func (c *negativeResumeCache) isBlocked(sandboxID string, now time.Time) bool {
	if c == nil {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	until, ok := c.exp[sandboxID]
	if !ok {
		return false
	}

	if now.After(until) {
		delete(c.exp, sandboxID)

		return false
	}

	return true
}

func (c *negativeResumeCache) block(sandboxID string, now time.Time) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.exp[sandboxID] = now.Add(notResumableNegativeTTL)
	c.mu.Unlock()
}
