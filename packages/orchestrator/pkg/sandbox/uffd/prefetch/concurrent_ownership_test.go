//go:build linux

package prefetch

import (
	"bytes"
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConcurrentStartsDoNotRetainPayloadBeforeReady(t *testing.T) { //nolint:paralleltest // Reads process-wide heap statistics after forced GC.
	const (
		starts    = 8
		pages     = 16
		blockSize = int64(1 << 20)
	)
	flags := newPrefetchTestFlags(t, 1, 0)
	indices := make([]uint64, pages+1)
	for i := range indices {
		indices[i] = uint64(i)
	}
	type run struct {
		start   func() error
		backend *startBackend
		queued  chan struct{}
		cancel  context.CancelFunc
		done    chan error
	}
	runs := make([]run, starts)
	for i := range runs {
		queued := make(chan struct{})
		source := &startSource{blockSize: blockSize}
		source.slice = func(ctx context.Context, off, length int64, _ uint64) ([]byte, error) {
			if off == pages*blockSize {
				// The sole fetch worker has queued every preceding page before
				// entering this call. Hold it here without owning another payload.
				close(queued)
				<-ctx.Done()

				return nil, ctx.Err()
			}

			return bytes.Repeat([]byte{0xa5}, int(length)), nil
		}
		backend := &startBackend{ready: make(chan struct{}), readyCalled: make(chan struct{})}
		ctx, cancel := context.WithCancel(t.Context())
		p := newStartPrefetcher(t, source, backend, flags, indices)
		runs[i] = run{
			start:   func() error { return p.Start(ctx) },
			backend: backend,
			queued:  queued,
			cancel:  cancel,
			done:    make(chan error, 1),
		}
	}

	runtime.GC() //nolint:revive // Distinguish retained payload from garbage in the live-heap measurement.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, r := range runs {
		go func() { r.done <- r.start() }()
	}
	defer func() {
		for _, r := range runs {
			r.cancel()
		}
		for _, r := range runs {
			waitStart(t, r.done)
		}
	}()
	for _, r := range runs {
		waitSignal(t, r.backend.readyCalled, "copy coordinator Ready wait")
		waitSignal(t, r.queued, "fetch worker past all queued pages")
		require.Zero(t, r.backend.prefaults.Load(), "nothing is installed before Ready")
	}
	runtime.GC() //nolint:revive // Distinguish retained payload from garbage in the live-heap measurement.
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(runs)
	liveGrowth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("%d starts x %d x 1MiB: live heap growth after GC=%d bytes", starts, pages, liveGrowth)
	require.Less(t, liveGrowth, int64(16<<20), "concurrent pre-Ready starts retained their 128MiB of fetched payload")
}
