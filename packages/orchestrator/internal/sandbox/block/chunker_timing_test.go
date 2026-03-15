package block

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestTimingSliceAfterPartialRequest compares Slice latency between
// StreamingChunker and FullFetchChunker using the same upstream and
// identical test flow:
//
//  1. Slice(0, blockSize)          — triggers the full-chunk fetch
//  2. Slice(lastOff, blockSize)    — timed; must wait for fetch to complete
//
// StreamingChunker fetches in a background goroutine; the second Slice
// blocks in registerAndWait. FullFetchChunker fetches synchronously on
// the first Slice; the second Slice is a cache hit.
//
// On CI, if StreamingChunker shows >1s warnings but FullFetchChunker
// does not, goroutine scheduling starvation is the cause. If both show
// warnings, it's general CI resource contention.
func TestTimingSliceAfterPartialRequest(t *testing.T) {
	t.Parallel()

	const (
		blockSize    = header.PageSize // 4KB
		sliceTimeout = 500 * time.Millisecond
		iterations   = 100
	)

	data := makeTestData(t, storage.MemoryChunkSize)

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)

	type chunkerFactory struct {
		name string
		make func(t *testing.T) Chunker
	}

	upstream := &fastUpstream{data: data, blockSize: blockSize}

	factories := []chunkerFactory{
		{
			name: "streaming",
			make: func(t *testing.T) Chunker {
				t.Helper()
				c, err := NewStreamingChunker(
					int64(len(data)), blockSize,
					upstream, t.TempDir()+"/cache",
					m, 0, nil,
				)
				require.NoError(t, err)

				return c
			},
		},
		{
			name: "full-fetch",
			make: func(t *testing.T) Chunker {
				t.Helper()
				c, err := NewFullFetchChunker(
					int64(len(data)), blockSize,
					upstream, t.TempDir()+"/cache",
					m,
				)
				require.NoError(t, err)

				return c
			},
		},
	}

	for _, f := range factories {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()

			for i := range iterations {
				t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
					t.Parallel()

					chunker := f.make(t)
					defer chunker.Close()

					// First Slice: triggers the fetch (background goroutine
					// for streaming, synchronous ReadAt for full-fetch).
					t0 := time.Now()
					_, err := chunker.Slice(t.Context(), 0, blockSize)
					firstSlice := time.Since(t0)
					require.NoError(t, err)

					lastOff := int64(storage.MemoryChunkSize) - blockSize

					ctx, cancel := context.WithTimeout(t.Context(), sliceTimeout)
					defer cancel()

					start := time.Now()
					slice, err := chunker.Slice(ctx, lastOff, blockSize)
					elapsed := time.Since(start)

					if err != nil {
						t.Fatalf("%s: Slice(%d) failed after %v (first Slice took %v): %v",
							f.name, lastOff, elapsed, firstSlice, err)
					}
					require.True(t, bytes.Equal(data[lastOff:lastOff+blockSize], slice),
						"data mismatch at offset %d", lastOff)

					if elapsed > 100*time.Millisecond {
						t.Logf("SLOW: %s first_slice=%v second_slice=%v",
							f.name, firstSlice, elapsed)
					}
				})
			}
		})
	}
}
