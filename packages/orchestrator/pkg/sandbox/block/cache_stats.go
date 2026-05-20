//go:build linux

package block

import (
	"context"
	"sync/atomic"
)

// CacheStats counts chunker cache hits vs misses over a logical operation
// (typically a single dedup run). Attach via WithCacheStats; Chunker.Slice
// increments via cacheStatsFromContext. Absent collector is a no-op.
type CacheStats struct {
	Hits   atomic.Int64
	Misses atomic.Int64
}

type cacheStatsKey struct{}

func WithCacheStats(ctx context.Context, s *CacheStats) context.Context {
	if s == nil {
		return ctx
	}

	return context.WithValue(ctx, cacheStatsKey{}, s)
}

func cacheStatsFromContext(ctx context.Context) *CacheStats {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(cacheStatsKey{}).(*CacheStats)

	return s
}
