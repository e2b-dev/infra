package userfaultfd

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils/testharness"
)

// matrixModes are the (subtest-name, removeEnabled) pairs that every
// generic cross-process test should iterate over so we have regression
// coverage for both the no-REMOVE and the REMOVE-enabled paths.
var matrixModes = []struct {
	name          string
	removeEnabled bool
}{
	{"remove-off", false},
	{"remove-on", true},
}

// runMatrix runs body once per matrix mode as a parallel subtest. The
// body is given a fresh testConfig that has the per-mode flag set so it
// can hand it straight to configureCrossProcessTest.
func runMatrix(t *testing.T, tt testConfig, body func(t *testing.T, cfg testConfig)) {
	t.Helper()

	for _, m := range matrixModes {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()

			cfg := tt
			cfg.removeEnabled = m.removeEnabled
			body(t, cfg)
		})
	}
}

// remove-only assert helper used by REMOVE-specific tests.
func removeOffset(offsets []uint, target uint) []uint {
	result := make([]uint, 0, len(offsets))
	for _, o := range offsets {
		if o != target {
			result = append(result, o)
		}
	}

	return result
}

type testConfig struct {
	name string
	// Page size of the memory area.
	pagesize uint64
	// Number of pages in the memory area.
	numberOfPages uint64
	// Operations to trigger on the memory area.
	operations []operation
	// alwaysWP makes the handler copy with UFFDIO_COPY_MODE_WP for all faults.
	alwaysWP bool
	// removeEnabled toggles UFFD_FEATURE_EVENT_REMOVE in configureApi.
	// Generic tests run in both modes via runMatrix; REMOVE-specific
	// tests pin removeEnabled=true.
	removeEnabled bool
	// gated is a documentation tag for tests that drive the handler's
	// pause/resume RPC explicitly. The harness itself doesn't read it;
	// it exists so the test author and reviewers can tell at a glance
	// that the test orchestrates serve pauses.
	gated bool
	// barriers enables the per-worker fault hook (race tests only).
	barriers bool
	// sourcePatcher is invoked on the random source data after it's
	// generated but before it's written to the content file the child
	// reads. Race tests use it to plant a deterministic sentinel.
	sourcePatcher func([]byte)
}

type operationMode uint32

const (
	operationModeRead operationMode = 1 << iota
	operationModeWrite
	operationModeRemove
	operationModeServePause
	operationModeServeResume
	// operationModeSleep pauses for a short duration to let async goroutines
	// enter their blocking syscalls before proceeding.
	operationModeSleep
)

type operation struct {
	// Offset in bytes. Must be smaller than the (numberOfPages-1) * pagesize as it reads a page and it must be aligned to the pagesize from the testConfig.
	offset int64
	mode   operationMode
	// async runs the operation in a background goroutine.
	async bool
}

type handlerPageStates struct {
	faulted []uint
	removed []uint
}

// allAccessed returns the sorted union of faulted+removed offsets. Each
// per-state slice is already sorted and the states are disjoint, so a
// simple merge suffices.
func (s handlerPageStates) allAccessed() []uint {
	out := make([]uint, 0, len(s.faulted)+len(s.removed))
	i, j := 0, 0
	for i < len(s.faulted) && j < len(s.removed) {
		if s.faulted[i] <= s.removed[j] {
			out = append(out, s.faulted[i])
			i++
		} else {
			out = append(out, s.removed[j])
			j++
		}
	}
	out = append(out, s.faulted[i:]...)
	out = append(out, s.removed[j:]...)

	return out
}

type testHandler struct {
	memoryArea *[]byte
	pagesize   uint64
	data       *MemorySlicer
	client     *testharness.Client
	mutex      sync.RWMutex
}

func (h *testHandler) pageStates() (handlerPageStates, error) {
	entries, err := h.client.PageStates()
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

func (h *testHandler) installFaultBarrier(_ context.Context, addr uintptr, phase faultPhase) (uint64, error) {
	return h.client.InstallBarrier(addr, testharness.Point(phase))
}

// waitFaultHeld blocks until the child reports a worker has reached the
// barrier identified by token, or ctx is cancelled. net/rpc's Call
// doesn't take a context, so we run it in a goroutine and race it.
func (h *testHandler) waitFaultHeld(ctx context.Context, token uint64) error {
	errCh := make(chan error, 1)
	go func() { errCh <- h.client.WaitFaultHeld(token) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *testHandler) releaseFault(_ context.Context, token uint64) error {
	return h.client.ReleaseFault(token)
}

func (h *testHandler) executeAll(t *testing.T, operations []operation) {
	t.Helper()

	var asyncErrors []chan error

	for i, op := range operations {
		if op.async {
			errCh := make(chan error, 1)
			asyncErrors = append(asyncErrors, errCh)

			go func() {
				errCh <- h.executeOperation(t.Context(), op)
			}()

			continue
		}

		err := h.executeOperation(t.Context(), op)
		require.NoError(t, err, "step %d: %v at offset %d", i, op.mode, op.offset)
	}

	for _, errCh := range asyncErrors {
		select {
		case err := <-errCh:
			require.NoError(t, err, "async operation")
		case <-t.Context().Done():
			t.Fatal("timed out waiting for async operation")
		}
	}
}

type pageExpectation uint8

const (
	expectClean   pageExpectation = iota // read-only: present + WP set
	expectDirty                          // written: present + WP cleared
	expectRemoved                        // removed: not present
)

func (h *testHandler) checkDirtiness(t *testing.T, operations []operation) {
	t.Helper()

	pagemap, err := testutils.NewPagemapReader()
	require.NoError(t, err)
	defer pagemap.Close()

	memStart := uintptr(unsafe.Pointer(&(*h.memoryArea)[0]))

	// Track the final expected state per offset by replaying operations in order.
	// A remove after a read/write makes the page not present.
	// A read/write after a remove makes it present again.
	expected := make(map[uint]pageExpectation)

	for _, op := range operations {
		off := uint(op.offset)
		switch op.mode {
		case operationModeRead:
			curr, seen := expected[off]
			// If we haven't seen this page before or the page
			// has previously been removed then the page should be clean
			// after this read operation.
			if !seen || curr == expectRemoved {
				expected[off] = expectClean
			}
		case operationModeWrite:
			expected[off] = expectDirty
		case operationModeRemove:
			expected[off] = expectRemoved
		}
	}

	for off, expect := range expected {
		entry, err := pagemap.ReadEntry(memStart + uintptr(off))
		require.NoError(t, err, "pagemap read at offset %d", off)

		switch expect {
		case expectRemoved:
			assert.False(t, entry.IsPresent(), "removed page at offset %d should not be present", off)
		case expectDirty:
			assert.True(t, entry.IsPresent(), "written page at offset %d should be present", off)
			assert.False(t, entry.IsWriteProtected(), "written page at offset %d should be dirty", off)
		case expectClean:
			assert.True(t, entry.IsPresent(), "read-only page at offset %d should be present", off)
			assert.True(t, entry.IsWriteProtected(), "read-only page at offset %d should be clean", off)
		}
	}
}

func (h *testHandler) executeOperation(ctx context.Context, op operation) error {
	switch op.mode {
	case operationModeRead:
		return h.executeRead(ctx, op)
	case operationModeWrite:
		return h.executeWrite(ctx, op)
	case operationModeRemove:
		return h.executeRemove(op)
	case operationModeServePause:
		return h.client.Pause()
	case operationModeServeResume:
		return h.client.Resume()
	case operationModeSleep:
		time.Sleep(50 * time.Millisecond)

		return nil
	default:
		return fmt.Errorf("invalid operation mode: %d", op.mode)
	}
}

func (h *testHandler) executeRemove(op operation) error {
	page := (*h.memoryArea)[op.offset : op.offset+int64(h.pagesize)]

	return unix.Madvise(page, unix.MADV_DONTNEED)
}

func (h *testHandler) executeRead(ctx context.Context, op operation) error {
	expectedBytes, err := h.data.Slice(ctx, op.offset, int64(h.pagesize))
	if err != nil {
		return err
	}

	// Hold the read side of the memoryArea mutex while we touch the page.
	// Reads can run concurrently with each other (RLock), but a parallel
	// async write to the same page (executeWrite, write lock) is excluded
	// so go test -race stays clean when a test plan mixes async read and
	// async write at overlapping offsets.
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	readBytes := (*h.memoryArea)[op.offset : op.offset+int64(h.pagesize)]

	// The bytes.Equal is the first place in this flow that actually touches the uffd managed memory and triggers the pagefault, so any deadlocks will manifest here.
	if !bytes.Equal(readBytes, expectedBytes) {
		idx, want, got := testutils.FirstDifferentByte(readBytes, expectedBytes)

		return fmt.Errorf("content mismatch: want '%x, got %x at index %d", want, got, idx)
	}

	return nil
}

func (h *testHandler) executeWrite(ctx context.Context, op operation) error {
	bytesToWrite, err := h.data.Slice(ctx, op.offset, int64(h.pagesize))
	if err != nil {
		return err
	}

	// An unprotected parallel write to map might result in an undefined behavior.
	// Lock excludes both other writers and any concurrent executeRead.
	h.mutex.Lock()
	defer h.mutex.Unlock()

	n := copy((*h.memoryArea)[op.offset:op.offset+int64(h.pagesize)], bytesToWrite)
	if n != int(h.pagesize) {
		return fmt.Errorf("copy length mismatch: want %d, got %d", h.pagesize, n)
	}

	return nil
}

func getOperationsOffsets(ops []operation, m operationMode) []uint {
	b := roaring.New()

	for _, operation := range ops {
		if operation.mode&m != 0 {
			b.Add(uint32(operation.offset))
		}
	}

	result := make([]uint, 0, b.GetCardinality())
	b.Iterate(func(x uint32) bool {
		result = append(result, uint(x))

		return true
	})

	return result
}
