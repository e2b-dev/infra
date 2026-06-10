//go:build linux

package prefetch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// prefaultResult scripts one Prefault call of fakeBackend.
type prefaultResult struct {
	installed bool
	err       error
}

// fakeBackend implements only Prefault; the embedded nil interface panics on
// anything else copyWorker shouldn't touch.
type fakeBackend struct {
	uffd.MemoryBackend

	results chan prefaultResult
}

func (f *fakeBackend) Prefault(context.Context, int64, []byte) (bool, error) {
	r := <-f.results

	return r.installed, r.err
}

func newTestPrefetcher(t *testing.T, results ...prefaultResult) *Prefetcher {
	t.Helper()

	log, err := logger.NewDevelopmentLogger()
	require.NoError(t, err)

	ch := make(chan prefaultResult, len(results))
	for _, r := range results {
		ch <- r
	}

	return &Prefetcher{logger: log, uffd: &fakeBackend{results: ch}}
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("copyWorker did not return within budget")
	}
}

// ErrClosed (uffd gone: sandbox teardown) must cancel the whole run so fetch
// workers stop fetching and queueing pages nobody will copy, and must count
// nothing.
func TestCopyWorkerCancelsRunOnErrClosed(t *testing.T) {
	t.Parallel()

	p := newTestPrefetcher(t, prefaultResult{err: userfaultfd.ErrClosed})

	ctx, cancelRun := context.WithCancel(t.Context())
	defer cancelRun()

	copyCh := make(chan prefetchData, 2)
	copyCh <- prefetchData{}
	copyCh <- prefetchData{} // must not be drained after ErrClosed

	var copied, skipped atomic.Uint64

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.copyWorker(ctx, cancelRun, copyCh, &copied, &skipped)
	}()

	waitDone(t, done)

	require.Error(t, ctx.Err(), "run context must be cancelled so fetch workers stop")
	require.Zero(t, copied.Load())
	require.Zero(t, skipped.Load())
	require.Len(t, copyCh, 1, "remaining queued pages must not be drained")
}

// Only installed pages count as copied; nil-error no-op prefaults (already
// resident / lost install race / deferred) and errors land in skipped, so
// stage="copied" matches prefault{result="installed"}.
func TestCopyWorkerCountsOnlyInstalledAsCopied(t *testing.T) {
	t.Parallel()

	p := newTestPrefetcher(t,
		prefaultResult{installed: true},               // copied
		prefaultResult{installed: false},              // no-op (skipped/present/deferred)
		prefaultResult{err: context.DeadlineExceeded}, // error
		prefaultResult{installed: true},               // copied
	)

	ctx, cancelRun := context.WithCancel(t.Context())
	defer cancelRun()

	copyCh := make(chan prefetchData, 4)
	for range 4 {
		copyCh <- prefetchData{}
	}
	close(copyCh)

	var copied, skipped atomic.Uint64

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.copyWorker(ctx, cancelRun, copyCh, &copied, &skipped)
	}()

	waitDone(t, done)

	require.NoError(t, ctx.Err(), "non-ErrClosed outcomes must not cancel the run")
	require.EqualValues(t, 2, copied.Load())
	require.EqualValues(t, 2, skipped.Load())
}
