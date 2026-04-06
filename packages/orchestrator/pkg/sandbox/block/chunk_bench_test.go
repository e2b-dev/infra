package block

import (
	"context"
	"path/filepath"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
)

const (
	cbBlockSize int64 = 4096
	cbNumBlocks int64 = 16384 // 64 MiB
	cbCacheSize int64 = cbNumBlocks * cbBlockSize
)

// BenchmarkChunkerSlice_CacheHit benchmarks the full FullFetchChunker.Slice
// hot path on a cache hit: bitmap check + mmap slice return + OTEL
// timer.Success with attribute construction.
func BenchmarkChunkerSlice_CacheHit(b *testing.B) {
	provider := sdkmetric.NewMeterProvider()
	b.Cleanup(func() { provider.Shutdown(context.Background()) })

	m, err := blockmetrics.NewMetrics(provider)
	if err != nil {
		b.Fatal(err)
	}

	chunker, err := NewFullFetchChunker(
		cbCacheSize, cbBlockSize,
		nil, // base is never called on cache hit
		filepath.Join(b.TempDir(), "cache"),
		m,
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { chunker.Close() })

	// Pre-populate the cache so every Slice hits.
	chunker.cache.setIsCached(0, cbCacheSize)

	ctx := context.Background()

	b.ResetTimer()
	for i := range b.N {
		off := int64(i%int(cbNumBlocks)) * cbBlockSize
		s, sliceErr := chunker.Slice(ctx, off, cbBlockSize)
		if sliceErr != nil {
			b.Fatal(sliceErr)
		}
		if len(s) == 0 {
			b.Fatal("empty slice")
		}
	}
}
