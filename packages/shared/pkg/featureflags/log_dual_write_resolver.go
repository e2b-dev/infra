package featureflags

import (
	"context"
	"sync"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
)

const logsDualWriteCacheTTL = time.Second

// LogsDualWriteResolver caches dual-write decisions.
type LogsDualWriteResolver struct {
	ff  *Client
	ttl time.Duration

	mu     sync.RWMutex
	cached bool
	expiry time.Time
	loaded bool
}

// NewLogsDualWriteResolver builds a cached resolver.
func NewLogsDualWriteResolver(ff *Client) *LogsDualWriteResolver {
	return newLogsDualWriteResolverWithTTL(ff, logsDualWriteCacheTTL)
}

func newLogsDualWriteResolverWithTTL(ff *Client, ttl time.Duration) *LogsDualWriteResolver {
	return &LogsDualWriteResolver{ff: ff, ttl: ttl}
}

// Resolve returns the cached flag.
func (r *LogsDualWriteResolver) Resolve(ctx context.Context, contexts ...ldcontext.Context) bool {
	if r.ff == nil {
		return false
	}

	now := time.Now()
	r.mu.RLock()
	if r.loaded && now.Before(r.expiry) {
		value := r.cached
		r.mu.RUnlock()

		return value
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.loaded && now.Before(r.expiry) {
		return r.cached
	}

	r.cached = r.ff.BoolFlag(ctx, LogsDualWriteFlag, contexts...)
	r.expiry = now.Add(r.ttl)
	r.loaded = true

	return r.cached
}
