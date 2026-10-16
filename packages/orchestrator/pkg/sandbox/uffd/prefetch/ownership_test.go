//go:build linux

package prefetch

import (
	"bytes"
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type ownedPayloadSource struct {
	blockSize int64
	calls     atomic.Uint64
}

func (s *ownedPayloadSource) BlockSize() int64 { return s.blockSize }

func (s *ownedPayloadSource) Slice(_ context.Context, _, length int64) ([]byte, error) {
	s.calls.Add(1)

	return bytes.Repeat([]byte{0xa5}, int(length)), nil
}

type delayedReadyBackend struct {
	uffd.MemoryBackend

	ready    chan struct{}
	waiting  chan struct{}
	prefault atomic.Uint64
}

func (b *delayedReadyBackend) Ready() chan struct{} {
	close(b.waiting)

	return b.ready
}

func (b *delayedReadyBackend) Prefault(_ context.Context, _ int64, data []byte) (bool, error) {
	b.prefault.Add(1)

	return len(data) > 0, nil
}

// Hold the actual copy coordinator at Ready while the actual fetch worker fills
// its queue. HeapAlloc after GC distinguishes queued live payload from transient
// source allocations. This is a queue-lifetime regression, not a process-wide
// peak-memory or runtime-feature-flag upper bound.
func TestPrefetchOffsetsDoNotRetainPayloadBeforeReady(t *testing.T) { //nolint:paralleltest // Reads process-wide heap statistics after forced GC.
	for _, coalesced := range []bool{false, true} { //nolint:paralleltest // Subtests share process-wide heap statistics.
		name := "pages"
		if coalesced {
			name = "coalesced-extent"
		}
		t.Run(name, func(t *testing.T) {
			const blockSize = int64(2 << 20)
			const pages = 64
			log, err := logger.NewDevelopmentLogger()
			require.NoError(t, err)
			source := &ownedPayloadSource{blockSize: blockSize}
			backend := &delayedReadyBackend{ready: make(chan struct{}), waiting: make(chan struct{})}
			p := &Prefetcher{logger: log, source: source, uffd: backend}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			copyCh := make(chan prefetchData, pages)
			var copyStart atomic.Pointer[time.Time]
			var copied, copySkipped atomic.Uint64
			done := make(chan struct{})
			go func() {
				defer close(done)
				p.startCopyWorkers(ctx, cancel, copyCh, 8, blockSize, &copyStart, &copied, &copySkipped)
			}()
			select {
			case <-backend.waiting:
			case <-time.After(5 * time.Second):
				t.Fatal("copy coordinator never reached Ready")
			}

			fetchCh := make(chan extent, pages)
			if coalesced {
				fetchCh <- extent{startIdx: 0, blocks: pages}
			} else {
				for i := range pages {
					fetchCh <- extent{startIdx: uint64(i), blocks: 1}
				}
			}
			close(fetchCh)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			var fetched, skipped atomic.Uint64
			p.fetchWorker(ctx, fetchCh, copyCh, blockSize, &fetched, &skipped)
			require.EqualValues(t, pages, fetched.Load())
			require.Zero(t, skipped.Load())
			require.Len(t, copyCh, pages)
			require.Zero(t, backend.prefault.Load(), "nothing is installed before Ready")
			runtime.GC()
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(copyCh)
			liveGrowth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			t.Logf("64 x 2MiB, coalesced=%v: live heap growth after GC=%d bytes, source calls=%d", coalesced, liveGrowth, source.calls.Load())
			require.Less(t, liveGrowth, int64(8<<20), "the pre-Ready queue retained owned page/extent payload")

			cancel()
			waitDone(t, done)
			require.Zero(t, backend.prefault.Load())
			require.Nil(t, copyStart.Load(), "cancel before Ready must not start copy workers")
		})
	}
}
