package userfaultfd

import (
	"context"
	"fmt"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// raceHappyPathBudget bounds every race test in this file. The whole
// point of these tests is that they detect a regression as a fast,
// targeted assertion rather than as a CI -timeout 30m hang. None of
// these tests should approach this budget on a healthy build.
const raceHappyPathBudget = 5 * time.Second

// barrierArrivalDeadline is how long the test will wait for a worker
// to reach an installed barrier. The hook fires the first thing in
// the worker goroutine, so on a healthy build it's a sub-millisecond
// rendezvous over the unix-socket RPC. Anything approaching this
// deadline means the handler dispatch is wedged.
const barrierArrivalDeadline = 2 * time.Second

// madviseBudget is how long we allow MADV_DONTNEED to spend in the
// kernel after we've parked a worker mid-handler. The fix guarantees
// madvise unblocks as soon as the handler drains the REMOVE event
// from the uffd fd, regardless of any worker holding RLock —
// readEvents requires no lock.
const madviseBudget = 2 * time.Second

// withRaceContext bounds a single race test to raceHappyPathBudget,
// failing with a clear "deadlock" message if the budget is exceeded.
func withRaceContext(t *testing.T, body func(ctx context.Context)) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), raceHappyPathBudget)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		body(ctx)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("race test exceeded happy-path budget of %s — handler is wedged", raceHappyPathBudget)
	}
}

// TestStaleSourceRaceMissingAndRemove is the deterministic regression
// test for the production fix in Serve():
//
//   - Pre-fix the parent serve loop captured `state == missing` and
//     `source = u.src` BEFORE handing the work to a worker goroutine.
//     A REMOVE event for the same page that arrived between then and
//     the worker actually running would silently leave the worker
//     with a stale `source = u.src` snapshot, which it would then
//     UFFDIO_COPY into the page that the kernel had just unmapped.
//
//   - Post-fix the worker reads pageTracker state INSIDE the
//     goroutine, under settleRequests.RLock, atomically with the
//     decision of which `source` to use.
//
// The test installs a barrierBeforeRLock on page X (so the worker
// for X parks before it can read state), triggers a MISSING-write
// fault on X from the parent, waits for the worker to park, fires
// MADV_DONTNEED on X (which can take settleRequests.Lock immediately
// — no worker holds RLock), and then releases the worker. After
// release the worker, post-fix, observes state=removed under RLock
// and zero-faults; pre-fix it would have UFFDIO_COPY'd the planted
// sentinel byte from u.src. A direct read of the page contents
// distinguishes the two outcomes deterministically.
func TestStaleSourceRaceMissingAndRemove(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pagesize uint64
	}{
		{name: "4k", pagesize: header.PageSize},
		{name: "hugepage", pagesize: header.HugepageSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			withRaceContext(t, func(ctx context.Context) {
				// Plant a deterministic, non-zero sentinel as the
				// first byte of the source data for the page we'll
				// race on. Pre-fix, the worker would UFFDIO_COPY this
				// sentinel into the page after the REMOVE has already
				// unmapped it. Post-fix the worker reads
				// state == removed under RLock and zero-fills.
				const sentinel = byte(0xC3)
				const pageIdx = 1
				pageOffset := int64(pageIdx) * int64(tt.pagesize)

				cfg := testConfig{
					pagesize:      tt.pagesize,
					numberOfPages: 4,
					barriers:      true,
					removeEnabled: true,
					sourcePatcher: func(content []byte) {
						content[pageOffset] = sentinel
					},
				}

				h, err := configureCrossProcessTest(ctx, t, cfg)
				require.NoError(t, err)

				memStart := uintptr(unsafe.Pointer(&(*h.memoryArea)[0]))
				addr := memStart + uintptr(pageIdx)*uintptr(tt.pagesize)

				token, err := h.installFaultBarrier(ctx, addr, faultPhaseBeforeRLock)
				require.NoError(t, err)

				// Trigger a READ fault (NOT a write — a write would
				// overwrite the very byte we want to inspect to
				// distinguish the two outcomes). h.executeRead does
				// the touch + content check; we run it in a goroutine
				// because it blocks on the fault until we release the
				// barrier.
				readErrCh := make(chan error, 1)
				go func() {
					readErrCh <- h.executeRead(ctx, operation{offset: pageOffset, mode: operationModeRead})
				}()

				// Wait for the worker for `addr` to park at the
				// pre-RLock barrier.
				waitCtx, waitCancel := context.WithTimeout(ctx, barrierArrivalDeadline)
				err = h.waitFaultHeld(waitCtx, token)
				waitCancel()
				require.NoError(t, err, "worker for page %d (addr %#x) did not park at barrier", pageIdx, addr)

				// Fire MADV_DONTNEED on the same page from the
				// parent. The serve loop can take Lock immediately
				// because the parked worker has not yet acquired
				// RLock.
				err = h.executeRemove(operation{offset: pageOffset, mode: operationModeRemove})
				require.NoError(t, err, "MADV_DONTNEED on page %d did not return — handler dispatch wedged", pageIdx)

				// Wait for the handler to commit setState(removed).
				// A tight poll loop with a hard deadline is used
				// rather than a sleep — the transition is
				// microseconds in the happy path.
				require.NoError(t, waitForState(ctx, h, uint64(pageOffset), removed, barrierArrivalDeadline),
					"handler did not transition page %d to `removed` after MADV_DONTNEED", pageIdx)

				// Release the parked worker. Post-fix it will
				// observe state == removed and zero-fault; pre-fix
				// it would proceed with the captured stale source.
				require.NoError(t, h.releaseFault(ctx, token))

				select {
				case err := <-readErrCh:
					// Pre-fix: executeRead's bytes.Equal succeeds
					// (page contains src bytes), so err == nil but
					// the page is observably wrong. Post-fix:
					// bytes.Equal fails (page is zero-filled), so
					// err != nil. We use the page-content assertion
					// below instead of relying on this side-channel.
					_ = err
				case <-ctx.Done():
					t.Fatalf("read of page %d did not unblock after barrier release", pageIdx)
				}

				// THE bug-detection assertion: post-fix the page
				// MUST be zero-filled. Pre-fix the worker
				// UFFDIO_COPY'd the planted sentinel.
				page := (*h.memoryArea)[pageOffset : pageOffset+int64(tt.pagesize)]
				assert.Equalf(t, byte(0), page[0],
					"page %d first byte: want 0 (post-fix zero-fault for `removed` state), got %#x — "+
						"if this equals the sentinel %#x, the worker used a stale `source = u.src` snapshot (regression)",
					pageIdx, page[0], sentinel,
				)

				// Sanity: verify with /proc/self/pagemap that the
				// page is in fact present after the racing read was
				// served (worker re-mapped it as zero).
				pagemap, err := testutils.NewPagemapReader()
				require.NoError(t, err)
				defer pagemap.Close()
				entry, err := pagemap.ReadEntry(addr)
				require.NoError(t, err)
				assert.True(t, entry.IsPresent(), "page %d should be present after the racing read", pageIdx)
			})
		})
	}
}

// TestNoMadviseDeadlockWithInflightCopy is a liveness regression test
// for the user-visible symptom that originally surfaced the stale-
// source race: the orchestrator's parent madvise(MADV_DONTNEED)
// blocking forever because the UFFD handler loop was wedged behind a
// worker.
//
// The harness parks the worker AFTER it has taken settleRequests.RLock
// AND captured `source` (i.e. as if its UFFDIO_COPY was in flight).
// From the parent we then issue MADV_DONTNEED on the same page and
// require that madvise returns within `madviseBudget`. madvise
// unblocks as soon as the handler's readEvents drains the REMOVE
// event, and readEvents requires no lock — so any future change that
// accidentally couples readEvents to settleRequests fails this test
// at the `madviseBudget` boundary instead of as a 30-minute CI
// timeout.
func TestNoMadviseDeadlockWithInflightCopy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pagesize uint64
	}{
		{name: "4k", pagesize: header.PageSize},
		{name: "hugepage", pagesize: header.HugepageSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			withRaceContext(t, func(ctx context.Context) {
				cfg := testConfig{
					pagesize:      tt.pagesize,
					numberOfPages: 4,
					barriers:      true,
					removeEnabled: true,
				}

				h, err := configureCrossProcessTest(ctx, t, cfg)
				require.NoError(t, err)

				const pageIdx = 2
				pageOffset := int64(pageIdx) * int64(tt.pagesize)

				memStart := uintptr(unsafe.Pointer(&(*h.memoryArea)[0]))
				addr := memStart + uintptr(pageIdx)*uintptr(tt.pagesize)

				token, err := h.installFaultBarrier(ctx, addr, faultPhaseBeforeFaultPage)
				require.NoError(t, err)

				writeErrCh := make(chan error, 1)
				go func() {
					writeErrCh <- h.executeWrite(ctx, operation{offset: pageOffset, mode: operationModeWrite})
				}()

				waitCtx, waitCancel := context.WithTimeout(ctx, barrierArrivalDeadline)
				err = h.waitFaultHeld(waitCtx, token)
				waitCancel()
				require.NoError(t, err, "worker for page %d (addr %#x) did not park at pre-COPY barrier", pageIdx, addr)

				// Worker is parked AFTER RLock. Issue MADV_DONTNEED
				// on the same page from the parent. The handler's
				// readEvents must drain the REMOVE event (so madvise
				// returns) even while the worker holds RLock.
				madviseDone := make(chan error, 1)
				go func() {
					madviseDone <- unix.Madvise((*h.memoryArea)[pageOffset:pageOffset+int64(tt.pagesize)], unix.MADV_DONTNEED)
				}()

				select {
				case err := <-madviseDone:
					require.NoError(t, err)
				case <-time.After(madviseBudget):
					_ = h.releaseFault(ctx, token)
					<-writeErrCh
					t.Fatalf("DEADLOCK: madvise(MADV_DONTNEED) on page %d did not return within %s "+
						"while a worker was parked holding settleRequests.RLock — readEvents must not require any lock",
						pageIdx, madviseBudget)
				}

				require.NoError(t, h.releaseFault(ctx, token))

				select {
				case err := <-writeErrCh:
					require.NoError(t, err)
				case <-ctx.Done():
					t.Fatalf("user-side write of page %d did not unblock after barrier release", pageIdx)
				}
			})
		})
	}
}

// TestFaultedShortCircuitOrdering uses the gated harness to
// deterministically queue a WRITE pagefault for a fresh page AND a
// REMOVE for an already-faulted page in the SAME serve-loop
// iteration. After resume, the post-batch state is asserted: the
// REMOVE'd page is `removed` and the racing-write page is `faulted`.
//
// Both pre-fix and post-fix code reach the same end state for this
// scenario (REMOVE batch runs before the pagefault dispatch loop in
// every Serve iteration). This test guards the batch-processing
// invariant itself: any future change that, for example, dispatched
// pagefaults before draining REMOVEs would fail this test as a
// concrete state-mismatch assertion rather than a 30-minute hang.
//
//nolint:paralleltest,tparallel // serialised: a paused gated handler keeps a faulting goroutine suspended in the kernel pagefault path; a STW GC pause from another parallel test would wait forever for that goroutine to reach a safe point.
func TestFaultedShortCircuitOrdering(t *testing.T) {
	tests := []struct {
		name     string
		pagesize uint64
	}{
		{name: "4k", pagesize: header.PageSize},
		{name: "hugepage", pagesize: header.HugepageSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { //nolint:paralleltest // see test-level comment
			withRaceContext(t, func(ctx context.Context) {
				cfg := testConfig{
					pagesize:      tt.pagesize,
					numberOfPages: 2,
					gated:         true,
					removeEnabled: true,
					operations: []operation{
						{offset: 0, mode: operationModeRead},
						{mode: operationModeServePause},
						{offset: 0, mode: operationModeRemove, async: true},
						{mode: operationModeSleep},
						{offset: int64(tt.pagesize), mode: operationModeWrite, async: true},
						{mode: operationModeSleep},
						{mode: operationModeServeResume},
					},
				}

				h, err := configureCrossProcessTest(ctx, t, cfg)
				require.NoError(t, err)

				h.executeAll(t, cfg.operations) //nolint:contextcheck // executeAll uses t.Context() per-op for the bounded race wrapper above

				states, err := h.pageStates()
				require.NoError(t, err)

				assert.Contains(t, states.removed, uint(0),
					"page 0 should be `removed` after REMOVE batch (got removed=%v faulted=%v)",
					states.removed, states.faulted,
				)
				assert.Contains(t, states.faulted, uint(tt.pagesize),
					"page 1 (offset %d) should be `faulted` after the racing write was served (got removed=%v faulted=%v)",
					tt.pagesize, states.removed, states.faulted,
				)
			})
		})
	}
}

// waitForState polls the child's PageStates RPC until the page at
// the given offset reaches `want` or `deadline` elapses. Each RPC
// round-trip is microseconds-to-low-milliseconds; we yield with a
// small sleep between polls so the harness doesn't burn an entire
// CPU on tight-loop encoding while the rest of the suite is also
// running cross-process tests.
func waitForState(ctx context.Context, h *testHandler, offset uint64, want pageState, deadline time.Duration) error {
	const pollInterval = 1 * time.Millisecond

	end := time.Now().Add(deadline)
	for {
		states, err := h.pageStates()
		if err != nil {
			return err
		}

		var bucket []uint
		switch want {
		case removed:
			bucket = states.removed
		case faulted:
			bucket = states.faulted
		}

		for _, off := range bucket {
			if uint64(off) == offset {
				return nil
			}
		}

		if time.Now().After(end) {
			return fmt.Errorf("page state at offset %d: want %d after %s — last seen removed=%v faulted=%v",
				offset, want, deadline, states.removed, states.faulted)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
