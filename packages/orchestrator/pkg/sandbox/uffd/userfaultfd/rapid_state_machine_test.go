package userfaultfd

// TestRapidStateMachine is a property-based state-machine fuzzer using
// pgregory.net/rapid. It drives randomised sequences of read / write /
// madvise(MADV_DONTNEED) actions against a live Userfaultfd handler running
// entirely within the parent process, and validates per-action invariants.
//
// Reproduce a failure:
//
//	RAPID_SEED=<seed-from-failure-output> go test -run=TestRapidStateMachine \
//	  ./pkg/sandbox/uffd/userfaultfd/...
//
// The dependency on pgregory.net/rapid is test-only; it does NOT appear in
// non-test builds (verified by: go list -deps ./... | grep pgregory).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"pgregory.net/rapid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// ─────────────────────────── directHandler ────────────────────────────────
//
// directHandler runs a live Userfaultfd handler entirely within the parent
// test process — no child RPC server required. This keeps each rapid case
// lightweight and avoids cross-process RPC latency for state queries.

type directHandler struct {
	mmap      []byte
	start     uintptr
	totalSize uint64
	pagesize  uint64
	data      *MemorySlicer // original source data for content verification
	uffdFd    Fd
	uffd      *Userfaultfd
	fdExit    *fdexit.FdExit
	// serveDone is closed once the Serve goroutine returns. Using close
	// (rather than a send) allows both the test body and t.Cleanup to select
	// on it concurrently without one consuming the value and starving the other.
	serveDone <-chan struct{}
	// serveErr holds the error returned by Serve (set before serveDone closes).
	serveErr *error
}

// newDirectHandler creates a UFFD handler in the calling (parent) process.
//
//   - mmap is created via testutils.NewPageMmap (munmap registered in t.Cleanup).
//   - A background goroutine runs Userfaultfd.Serve; t.Cleanup signals exit and
//     waits for it to drain before closing the fd and unregistering the range.
//   - src is the block.Slicer supplied to Serve (may be a chaosSource wrapper).
//   - data is the unwrapped MemorySlicer used for expected-content verification.
func newDirectHandler(t *testing.T, cfg testConfig, src block.Slicer, data *MemorySlicer) (*directHandler, error) {
	t.Helper()

	size := cfg.pagesize * cfg.numberOfPages

	mmap, start, err := testutils.NewPageMmap(t, size, cfg.pagesize)
	if err != nil {
		return nil, fmt.Errorf("NewPageMmap: %w", err)
	}

	uffdFd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("newFd: %w", err)
	}

	if err := configureApi(uffdFd, cfg.pagesize, cfg.removeEnabled); err != nil {
		_ = uffdFd.close()

		return nil, fmt.Errorf("configureApi: %w", err)
	}

	if err := register(uffdFd, start, size, UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP); err != nil {
		_ = uffdFd.close()

		return nil, fmt.Errorf("register: %w", err)
	}

	log, err := logger.NewDevelopmentLogger()
	if err != nil {
		_ = unregister(uffdFd, start, size)
		_ = uffdFd.close()

		return nil, fmt.Errorf("NewDevelopmentLogger: %w", err)
	}

	mapping := memory.NewMapping([]memory.Region{{
		BaseHostVirtAddr: start,
		Size:             uintptr(size),
		Offset:           0,
		PageSize:         uintptr(cfg.pagesize),
	}})

	uffd, err := NewUserfaultfdFromFd(uintptr(uffdFd), src, mapping, log)
	if err != nil {
		_ = unregister(uffdFd, start, size)
		_ = uffdFd.close()

		return nil, fmt.Errorf("NewUserfaultfdFromFd: %w", err)
	}

	exit, err := fdexit.New()
	if err != nil {
		_ = unregister(uffdFd, start, size)
		_ = uffdFd.close()

		return nil, fmt.Errorf("fdexit.New: %w", err)
	}

	done := make(chan struct{})
	var serveErrStore error

	dh := &directHandler{
		mmap:      mmap,
		start:     start,
		totalSize: size,
		pagesize:  cfg.pagesize,
		data:      data,
		uffdFd:    uffdFd,
		uffd:      uffd,
		fdExit:    exit,
		serveDone: done,
		serveErr:  &serveErrStore,
	}

	go func() {
		defer close(done)
		serveErrStore = uffd.Serve(context.Background(), exit)
	}()

	t.Cleanup(func() {
		// Best-effort shutdown; explicit callers may have already done this.
		_ = exit.SignalExit()
		select {
		case <-dh.serveDone:
		case <-time.After(10 * time.Second):
		}
		_ = exit.Close()
		_ = unregister(uffdFd, start, size)
		_ = uffdFd.close()
	})

	return dh, nil
}

// signalShutdown signals Serve to exit and returns the done channel. The
// channel is closed (not sent to) once Serve returns, so multiple concurrent
// waiters (test body + t.Cleanup) are all unblocked without data races.
func (dh *directHandler) signalShutdown() <-chan struct{} {
	_ = dh.fdExit.SignalExit()

	return dh.serveDone
}

// serveError returns the error from the Serve goroutine. Must only be called
// after serveDone has been closed (i.e., after the Serve goroutine has exited).
func (dh *directHandler) serveError() error {
	return *dh.serveErr
}

// closeUffdFd unregisters the range and closes the UFFD fd. Any goroutines
// still blocked in MADV_POPULATE_READ/WRITE with an unresolved fault receive
// EFAULT and can return from their syscall.
func (dh *directHandler) closeUffdFd() {
	_ = unregister(dh.uffdFd, dh.start, dh.totalSize)
	_ = dh.uffdFd.close()
}

// pageStates returns the current page-state snapshot from the handler's
// pageTracker, settling all in-flight copy workers first.
func (dh *directHandler) pageStates() (handlerPageStates, error) {
	entries, err := dh.uffd.pageStateEntries()
	if err != nil {
		return handlerPageStates{}, err
	}

	var states handlerPageStates

	for _, e := range entries {
		switch pageState(e.State) {
		case faulted:
			states.faulted = append(states.faulted, uint(e.Offset))
		case removed:
			states.removed = append(states.removed, uint(e.Offset))
		}
	}

	slices.Sort(states.faulted)
	slices.Sort(states.removed)

	return states, nil
}

// waitForDirectState polls pageStates until the page at offset reaches want or
// the deadline elapses.
func waitForDirectState(ctx context.Context, dh *directHandler, offset int64, want pageState, deadline time.Duration) error {
	const pollInterval = time.Millisecond

	end := time.Now().Add(deadline)

	for {
		states, err := dh.pageStates()
		if err != nil {
			return err
		}

		target := uint(offset)

		switch {
		case want == removed && slices.Contains(states.removed, target):
			return nil
		case want == faulted && slices.Contains(states.faulted, target):
			return nil
		}

		if time.Now().After(end) {
			return fmt.Errorf("page at offset %d: want %v after %s — last seen removed=%v faulted=%v",
				offset, want, deadline, states.removed, states.faulted)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// ─────────────────────── rapid state-machine ──────────────────────────────

// rapidPageState extends the handler's 3-state model with a 4th state to
// correctly track expected content after a remove + re-fault (which zero-fills
// instead of using the source slicer).
type rapidPageState uint8

const (
	rapidMissing     rapidPageState = iota // never faulted — content undefined
	rapidFaulted                           // faulted with source data
	rapidZeroFaulted                       // re-faulted after REMOVE — content zero
	rapidRemoved                           // removed via MADV_DONTNEED, not in memory
)

func (s rapidPageState) String() string {
	switch s {
	case rapidMissing:
		return "missing"
	case rapidFaulted:
		return "faulted"
	case rapidZeroFaulted:
		return "zero-faulted"
	case rapidRemoved:
		return "removed"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(s))
	}
}

// uffdStateMachine is a rapid.StateMachine. It drives a live directHandler
// with random read / write / madvise sequences and validates invariants.
//
// Model transitions:
//
//	Read:    missing/faulted → faulted (source content)
//	         zero-faulted   → zero-faulted (zero content, already in memory)
//	         removed        → zero-faulted (zero-filled on re-fault)
//	Write:   any            → faulted (source content applied by copy)
//	Madvise: faulted/zero-faulted → removed  (only when removeEnabled)
//	         missing/removed: skip (no REMOVE event would fire)
type uffdStateMachine struct {
	dh            *directHandler
	model         []rapidPageState // indexed by page number
	removeEnabled bool
}

// pageOffset converts a page index to its byte offset in the mmap.
func (sm *uffdStateMachine) pageOffset(idx int) int64 {
	return int64(idx) * int64(sm.dh.pagesize)
}

// Read selects a random page, pre-faults it via MADV_POPULATE_READ (STW-safe),
// and validates content against the model's expectation.
func (sm *uffdStateMachine) Read(rt *rapid.T) {
	idx := rapid.IntRange(0, len(sm.model)-1).Draw(rt, "idx")
	offset := sm.pageOffset(idx)
	page := sm.dh.mmap[offset : offset+int64(sm.dh.pagesize)]

	if err := unix.Madvise(page, unix.MADV_POPULATE_READ); err != nil {
		rt.Fatalf("page %d: MADV_POPULATE_READ: %v", idx, err)
	}

	switch sm.model[idx] {
	case rapidMissing, rapidFaulted:
		// Expect source data.
		expected, err := sm.dh.data.Slice(rt.Context(), offset, int64(sm.dh.pagesize))
		if err != nil {
			rt.Fatalf("page %d: data.Slice: %v", idx, err)
		}

		if !bytes.Equal(page, expected) {
			i, want, got := testutils.FirstDifferentByte(page, expected)
			rt.Fatalf("page %d (state=%v): content mismatch at byte %d: want %#x got %#x",
				idx, sm.model[idx], i, want, got)
		}

		sm.model[idx] = rapidFaulted

	default: // rapidZeroFaulted, rapidRemoved: expect zero fill from handler
		for i, b := range page {
			if b != 0 {
				rt.Fatalf("page %d (state=%v): expected zero at byte %d, got %#x",
					idx, sm.model[idx], i, b)
			}
		}

		sm.model[idx] = rapidZeroFaulted
	}
}

// Write selects a random page, pre-faults it for writing via
// MADV_POPULATE_WRITE (STW-safe), copies source data, and validates the
// transition to rapidFaulted.
func (sm *uffdStateMachine) Write(rt *rapid.T) {
	idx := rapid.IntRange(0, len(sm.model)-1).Draw(rt, "idx")
	offset := sm.pageOffset(idx)

	src, err := sm.dh.data.Slice(rt.Context(), offset, int64(sm.dh.pagesize))
	if err != nil {
		rt.Fatalf("page %d: data.Slice: %v", idx, err)
	}

	page := sm.dh.mmap[offset : offset+int64(sm.dh.pagesize)]

	if err := unix.Madvise(page, unix.MADV_POPULATE_WRITE); err != nil {
		rt.Fatalf("page %d: MADV_POPULATE_WRITE: %v", idx, err)
	}

	n := copy(page, src)
	if n != int(sm.dh.pagesize) {
		rt.Fatalf("page %d: copy length mismatch: want %d got %d", idx, sm.dh.pagesize, n)
	}

	// Write always stores source data regardless of previous state.
	sm.model[idx] = rapidFaulted
}

// Madvise issues MADV_DONTNEED on a randomly chosen page that the model
// believes is currently in memory (faulted or zero-faulted). Skips when
// removeEnabled is false or when the page is missing / already removed.
func (sm *uffdStateMachine) Madvise(rt *rapid.T) {
	if !sm.removeEnabled {
		rt.Skip("removeEnabled=false: REMOVE events are not enabled")
	}

	idx := rapid.IntRange(0, len(sm.model)-1).Draw(rt, "idx")
	if sm.model[idx] == rapidMissing || sm.model[idx] == rapidRemoved {
		rt.Skip("page not in memory — MADV_DONTNEED would not generate a REMOVE event")
	}

	offset := sm.pageOffset(idx)
	page := sm.dh.mmap[offset : offset+int64(sm.dh.pagesize)]

	if err := unix.Madvise(page, unix.MADV_DONTNEED); err != nil {
		rt.Fatalf("page %d: MADV_DONTNEED: %v", idx, err)
	}

	if err := waitForDirectState(rt.Context(), sm.dh, offset, removed, 5*time.Second); err != nil {
		rt.Fatalf("page %d: %v", idx, err)
	}

	sm.model[idx] = rapidRemoved
}

// Check is called after every action by rapid.Repeat. It verifies that the
// handler's pageTracker agrees with the model.
func (sm *uffdStateMachine) Check(rt *rapid.T) {
	states, err := sm.dh.pageStates()
	if err != nil {
		rt.Fatalf("pageStates: %v", err)
	}

	faultedSet := make(map[uint]struct{}, len(states.faulted))
	for _, off := range states.faulted {
		faultedSet[off] = struct{}{}
	}

	removedSet := make(map[uint]struct{}, len(states.removed))
	for _, off := range states.removed {
		removedSet[off] = struct{}{}
	}

	for idx, modelState := range sm.model {
		off := uint(sm.pageOffset(idx))
		_, isFaulted := faultedSet[off]
		_, isRemoved := removedSet[off]

		switch modelState {
		case rapidMissing:
			if isFaulted || isRemoved {
				rt.Fatalf("page %d (offset %d): model=missing but handler reports faulted=%v removed=%v",
					idx, off, isFaulted, isRemoved)
			}

		case rapidFaulted, rapidZeroFaulted:
			if !isFaulted {
				rt.Fatalf("page %d (offset %d): model=%v but handler not faulted (faulted=%v removed=%v)",
					idx, off, modelState, states.faulted, states.removed)
			}

		case rapidRemoved:
			if !isRemoved {
				rt.Fatalf("page %d (offset %d): model=removed but handler not removed (faulted=%v removed=%v)",
					idx, off, states.faulted, states.removed)
			}
		}
	}
}

// ────────────────────── TestRapidStateMachine ─────────────────────────────

// rapidPageCount is the number of pages per 4 K rapid case. Kept small enough
// that pageStateEntries() returns quickly while exercising inter-page
// interactions.
const rapidPageCount = 32

// TestRapidStateMachine is a property-based test that exercises the
// userfaultfd page-state machine under random read/write/madvise sequences.
//
// Run parameters (all overridable via rapid's standard env vars):
//
//	RAPID_SEED=N    — reproduce a specific failure verbatim
//	RAPID_CHECKS=N  — number of independent cases (default 100)
//	RAPID_STEPS=N   — average actions per case (default 30)
func TestRapidStateMachine(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("skipping: requires root (userfaultfd syscall)")
	}

	pagesizeCases := []struct {
		name     string
		pagesize uint64
		pages    uint64
	}{
		{"4k", header.PageSize, rapidPageCount},
		{"hugepage", header.HugepageSize, 8}, // 8×2MiB per case keeps memory bounded
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

				rapid.Check(t, func(rt *rapid.T) {
					data := RandomPages(cfg.pagesize, cfg.numberOfPages)

					dh, err := newDirectHandler(t, cfg, data, data)
					require.NoError(t, err, "newDirectHandler")

					// Explicit shutdown at end of property invocation so the
					// resources are released before the next case starts.
					// t.Cleanup still fires as a safety net if Fatalf aborts.
					defer func() { _ = dh.fdExit.SignalExit() }()

					sm := &uffdStateMachine{
						dh:            dh,
						model:         make([]rapidPageState, cfg.numberOfPages),
						removeEnabled: cfg.removeEnabled,
					}

					rt.Repeat(rapid.StateMachineActions(sm))
				})
			})
		})
	}
}
