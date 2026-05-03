package userfaultfd

// TestChaosCloseTerminatesUnderLatency verifies the Close↔Serve drain
// contract: even when the source slicer injects uniform random latency per
// Slice call (simulating slow storage), Shutdown must complete within a
// hard budget of 5 seconds.
//
// This guards against regressions where a slow worker wedges the Serve drain
// (e.g. the drain path acquires a lock held by an in-flight worker, or the
// worker's ctx is never cancelled so it blocks on the slow source forever).
//
// Reproduce a failure:
//
//	UFFD_CHAOS_SEED=<seed-from-log> go test -run=TestChaosCloseTerminatesUnderLatency \
//	  ./pkg/sandbox/uffd/userfaultfd/...

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	chaosMaxDelay       = 50 * time.Millisecond
	chaosShutdownBudget = 5 * time.Second
	chaosReaderCount    = 64
	chaosWarmupDelay    = 100 * time.Millisecond
)

// chaosSeedFromEnv returns the seed for the chaos RNG: the value of
// UFFD_CHAOS_SEED if set, otherwise time.Now().UnixNano().
func chaosSeedFromEnv(t *testing.T) int64 {
	t.Helper()

	if s := os.Getenv("UFFD_CHAOS_SEED"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("UFFD_CHAOS_SEED=%q: %v", s, err)
		}

		return v
	}

	return time.Now().UnixNano()
}

// TestChaosCloseTerminatesUnderLatency wraps block.Slicer with chaosSource
// (uniform random [0, 50ms] latency per Slice call), fires chaosReaderCount
// concurrent MADV_POPULATE_READ goroutines against the live handler, triggers
// shutdown after chaosWarmupDelay, and asserts:
//
//  1. Serve returns within chaosShutdownBudget.
//  2. All reader goroutines unblock within chaosShutdownBudget (errors due to
//     UFFD fd closure are expected and treated as non-fatal).
func TestChaosCloseTerminatesUnderLatency(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("skipping: requires root (userfaultfd syscall)")
	}

	seed := chaosSeedFromEnv(t)
	t.Logf("UFFD_CHAOS_SEED=%d", seed)

	pagesizeCases := []struct {
		name     string
		pagesize uint64
		pages    uint64
	}{
		{"4k", header.PageSize, 16},
		{"hugepage", header.HugepageSize, 4},
	}

	for _, pc := range pagesizeCases {
		t.Run(pc.name, func(t *testing.T) {
			t.Parallel()

			tt := testConfig{
				name:          pc.name,
				pagesize:      pc.pagesize,
				numberOfPages: pc.pages,
			}

			runMatrix(t, tt, func(t *testing.T, cfg testConfig) {
				t.Helper()
				runChaosCloseTest(t, cfg, seed)
			})
		})
	}
}

// runChaosCloseTest is the inner body for one (pagesize, removeMode) variant.
func runChaosCloseTest(t *testing.T, cfg testConfig, seed int64) {
	t.Helper()

	data := RandomPages(cfg.pagesize, cfg.numberOfPages)
	chaos := newChaosSource(data, seed, chaosMaxDelay)

	dh, err := newDirectHandler(t, cfg, chaos, data)
	require.NoError(t, err, "newDirectHandler")

	pageCount := int(cfg.numberOfPages)
	pagesize := cfg.pagesize

	// Fire chaosReaderCount concurrent MADV_POPULATE_READ goroutines.
	// Each goroutine faults a single page; the chaos source adds [0, 50ms]
	// latency to every Slice call inside Serve, causing in-flight workers to
	// hold the settle lock longer than usual.
	var g errgroup.Group

	for i := range chaosReaderCount {
		pageIdx := i % pageCount
		g.Go(func() error {
			offset := int64(pageIdx) * int64(pagesize)
			page := dh.mmap[offset : offset+int64(pagesize)]

			// MADV_POPULATE_READ pre-faults via _Gsyscall (STW-safe). It blocks
			// until the handler issues UFFDIO_COPY or the UFFD fd is closed.
			if err := unix.Madvise(page, unix.MADV_POPULATE_READ); err != nil {
				// EFAULT / EINTR after UFFD fd closure — expected during shutdown.
				return nil //nolint:nilerr // intentional: shutdown errors are not bugs
			}

			return nil
		})
	}

	// Allow some in-flight faults before triggering shutdown.
	time.Sleep(chaosWarmupDelay)

	shutdownAt := time.Now()

	// Signal Serve to drain and exit.
	serveDone := dh.signalShutdown()

	// Assert Serve returns within the budget.
	select {
	case <-serveDone:
		elapsed := time.Since(shutdownAt)
		require.NoError(t, dh.serveError(), "Serve returned with error after shutdown")
		t.Logf("Serve drained in %s (budget %s)", elapsed.Round(time.Millisecond), chaosShutdownBudget)

	case <-time.After(chaosShutdownBudget):
		t.Fatalf("TIMEOUT: Serve did not return within %s — Close↔Serve drain regression?",
			chaosShutdownBudget)
	}

	// Close the UFFD fd so goroutines still blocked in MADV_POPULATE_READ on
	// an unresolved fault (page queued but not dispatched before exit) receive
	// EFAULT and unblock.
	dh.closeUffdFd()

	// Wait for all reader goroutines; errors from fd closure are swallowed above.
	readersDone := make(chan error, 1)
	go func() { readersDone <- g.Wait() }()

	remaining := chaosShutdownBudget - time.Since(shutdownAt)
	if remaining <= 0 {
		remaining = time.Second
	}

	select {
	case err := <-readersDone:
		require.NoError(t, err, "reader goroutine returned unexpected error")

	case <-time.After(remaining):
		t.Fatalf("TIMEOUT: %d reader goroutines did not unblock within %s — deadlock?",
			chaosReaderCount, chaosShutdownBudget)
	}

	t.Logf("all %d reader goroutines unblocked (%s since shutdown)",
		chaosReaderCount, time.Since(shutdownAt).Round(time.Millisecond))

	t.Logf("chaos invariant OK: %s", fmt.Sprintf("pagesize=%d removeEnabled=%v",
		cfg.pagesize, cfg.removeEnabled))
}
