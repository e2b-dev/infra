//go:build linux

package prefetch

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func newPrefetchTestFlags(t *testing.T, copyWorkers, coalesceMiB int) *featureflags.Client {
	t.Helper()

	source := ldtestdata.DataSource()
	for _, setting := range []struct {
		key   string
		value int
	}{
		{featureflags.MemoryPrefetchMaxFetchWorkers.Key(), 1},
		{featureflags.MemoryPrefetchMaxCopyWorkers.Key(), copyWorkers},
		{featureflags.MemoryPrefetchCoalesceMaxMB.Key(), coalesceMiB},
	} {
		source.Update(source.Flag(setting.key).ValueForAll(ldvalue.Int(setting.value)))
	}
	flags, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, flags.Close(context.WithoutCancel(t.Context()))) })

	return flags
}

func newPrefetchTestLogger(t *testing.T) logger.Logger {
	t.Helper()

	log, err := logger.NewDevelopmentLogger()
	require.NoError(t, err)

	return log
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitStart(t *testing.T, done <-chan error) {
	t.Helper()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Prefetcher.Start did not return")
	}
}

func prefetchPayload(off, length int64) []byte {
	b := make([]byte, length)
	for i := range b {
		b[i] = byte(off + int64(i))
	}

	return b
}

type startSource struct {
	blockSize int64
	calls     atomic.Uint64
	bytes     atomic.Uint64
	slice     func(context.Context, int64, int64, uint64) ([]byte, error)
}

func (s *startSource) BlockSize() int64 { return s.blockSize }

func (s *startSource) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	call := s.calls.Add(1)
	s.bytes.Add(uint64(length))
	if s.slice != nil {
		return s.slice(ctx, off, length, call)
	}

	return prefetchPayload(off, length), nil
}

type startBackend struct {
	uffd.MemoryBackend

	ready       chan struct{}
	readyCalled chan struct{}
	readyOnce   sync.Once
	prefaults   atomic.Uint64
	prefault    func(context.Context, int64, []byte) (bool, error)
}

func (b *startBackend) Ready() chan struct{} {
	b.readyOnce.Do(func() { close(b.readyCalled) })

	return b.ready
}

func (b *startBackend) Prefault(ctx context.Context, off int64, data []byte) (bool, error) {
	b.prefaults.Add(1)
	if b.prefault != nil {
		return b.prefault(ctx, off, data)
	}

	return true, nil
}

func newStartPrefetcher(
	t *testing.T,
	source *startSource,
	backend *startBackend,
	flags *featureflags.Client,
	indices []uint64,
) *Prefetcher {
	t.Helper()

	return &Prefetcher{
		logger:       newPrefetchTestLogger(t),
		source:       source,
		uffd:         backend,
		mapping:      &metadata.MemoryPrefetchMapping{Indices: indices, BlockSize: source.blockSize},
		Prefault:     true,
		featureFlags: flags,
	}
}

func TestStartPrefaultZeroCopyWorkersCompletes(t *testing.T) {
	t.Parallel()

	const blockSize = int64(4096)
	source := &startSource{blockSize: blockSize}
	backend := &startBackend{ready: make(chan struct{}), readyCalled: make(chan struct{})}
	p := newStartPrefetcher(t, source, backend, newPrefetchTestFlags(t, 0, 0), []uint64{0, 1, 2})

	done := make(chan error, 1)
	go func() { done <- p.Start(t.Context()) }()
	waitStart(t, done)
	require.EqualValues(t, 3, source.calls.Load(), "fetch-only zero-copy mode must still warm every page")
	require.Zero(t, backend.prefaults.Load())
	select {
	case <-backend.readyCalled:
		t.Fatal("zero copy workers must not wait for UFFD Ready")
	default:
	}
}

func TestStartCancelsBeforeReadyAfterQueueingOffsets(t *testing.T) {
	t.Parallel()

	const blockSize = int64(4096)
	allFetched := make(chan struct{})
	source := &startSource{blockSize: blockSize}
	source.slice = func(_ context.Context, off, length int64, call uint64) ([]byte, error) {
		if call == 3 {
			close(allFetched)
		}

		return prefetchPayload(off, length), nil
	}
	backend := &startBackend{ready: make(chan struct{}), readyCalled: make(chan struct{})}
	p := newStartPrefetcher(t, source, backend, newPrefetchTestFlags(t, 1, 0), []uint64{0, 1, 2})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- p.Start(ctx) }()
	waitSignal(t, backend.readyCalled, "copy coordinator Ready wait")
	waitSignal(t, allFetched, "all pre-Ready fetches")
	cancel()
	waitStart(t, done)
	require.Zero(t, backend.prefaults.Load(), "cancel before Ready must not install a page")
}

func TestStartCancelsDuringFetch(t *testing.T) {
	t.Parallel()

	const blockSize = int64(4096)
	fetchEntered := make(chan struct{})
	source := &startSource{blockSize: blockSize}
	source.slice = func(ctx context.Context, _, _ int64, _ uint64) ([]byte, error) {
		close(fetchEntered)
		<-ctx.Done()

		return nil, ctx.Err()
	}
	backend := &startBackend{ready: make(chan struct{}), readyCalled: make(chan struct{})}
	close(backend.ready)
	p := newStartPrefetcher(t, source, backend, newPrefetchTestFlags(t, 1, 0), []uint64{0})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- p.Start(ctx) }()
	waitSignal(t, fetchEntered, "blocked fetch")
	cancel()
	waitStart(t, done)
	require.Zero(t, backend.prefaults.Load())
}

func TestStartCancelsDuringCopy(t *testing.T) {
	t.Parallel()

	const blockSize = int64(4096)
	copyEntered := make(chan struct{})
	source := &startSource{blockSize: blockSize}
	backend := &startBackend{ready: make(chan struct{}), readyCalled: make(chan struct{})}
	close(backend.ready)
	backend.prefault = func(ctx context.Context, _ int64, _ []byte) (bool, error) {
		close(copyEntered)
		<-ctx.Done()

		return false, ctx.Err()
	}
	p := newStartPrefetcher(t, source, backend, newPrefetchTestFlags(t, 1, 0), []uint64{0})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- p.Start(ctx) }()
	waitSignal(t, copyEntered, "blocked copy")
	cancel()
	waitStart(t, done)
	require.EqualValues(t, 1, backend.prefaults.Load())
}

func TestStartErrClosedCancelsInFlightFetch(t *testing.T) {
	t.Parallel()

	const blockSize = int64(4096)
	secondFetchEntered := make(chan struct{})
	secondFetchCanceled := make(chan struct{})
	source := &startSource{blockSize: blockSize}
	source.slice = func(ctx context.Context, off, length int64, _ uint64) ([]byte, error) {
		if off == blockSize {
			close(secondFetchEntered)
			<-ctx.Done()
			close(secondFetchCanceled)

			return nil, ctx.Err()
		}

		return prefetchPayload(off, length), nil
	}
	backend := &startBackend{ready: make(chan struct{}), readyCalled: make(chan struct{})}
	close(backend.ready)
	backend.prefault = func(context.Context, int64, []byte) (bool, error) {
		waitSignal(t, secondFetchEntered, "second fetch before ErrClosed")

		return false, userfaultfd.ErrClosed
	}
	p := newStartPrefetcher(t, source, backend, newPrefetchTestFlags(t, 1, 0), []uint64{0, 1})

	done := make(chan error, 1)
	go func() { done <- p.Start(t.Context()) }()
	waitSignal(t, secondFetchCanceled, "in-flight fetch cancellation")
	waitStart(t, done)
	require.EqualValues(t, 1, backend.prefaults.Load())
}

type installedPage struct {
	offset int64
	data   []byte
}

func TestStartCoalescedFetchRereadsExactPagesForInstall(t *testing.T) {
	t.Parallel()

	const blockSize = int64(1 << 20)
	source := &startSource{blockSize: blockSize}
	backend := &startBackend{ready: make(chan struct{}), readyCalled: make(chan struct{})}
	close(backend.ready)
	var (
		mu        sync.Mutex
		installed []installedPage
	)
	backend.prefault = func(_ context.Context, off int64, data []byte) (bool, error) {
		mu.Lock()
		installed = append(installed, installedPage{offset: off, data: bytes.Clone(data)})
		mu.Unlock()

		return true, nil
	}
	p := newStartPrefetcher(t, source, backend, newPrefetchTestFlags(t, 1, 2), []uint64{0, 1})
	require.NoError(t, p.Start(t.Context()))

	require.EqualValues(t, 3, source.calls.Load(), "one coalesced warm fetch plus one re-read per installed page")
	require.EqualValues(t, 4*blockSize, source.bytes.Load(), "the warm fetch plus one re-read per page reads two source bytes per installed byte")
	require.EqualValues(t, 2, backend.prefaults.Load())
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []installedPage{
		{offset: 0, data: prefetchPayload(0, blockSize)},
		{offset: blockSize, data: prefetchPayload(blockSize, blockSize)},
	}, installed)
}
