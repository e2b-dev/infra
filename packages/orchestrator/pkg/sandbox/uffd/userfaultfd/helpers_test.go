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

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils/testharness"
)

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
	// barriers enables the per-worker fault hook (race tests only).
	barriers bool
}

type operationMode uint32

const (
	operationModeRead operationMode = 1 << iota
	operationModeWrite
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
}

func (s handlerPageStates) allAccessed() []uint {
	return slices.Clone(s.faulted)
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
		if block.State(e.State) == block.Dirty {
			states.faulted = append(states.faulted, uint(e.Offset))
		}
	}
	slices.Sort(states.faulted)

	return states, nil
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
	expectClean pageExpectation = iota // read-only: present + WP set
	expectDirty                        // written: present + WP cleared
)

func (h *testHandler) checkDirtiness(t *testing.T, operations []operation) {
	t.Helper()

	pagemap, err := testutils.NewPagemapReader()
	require.NoError(t, err)
	defer pagemap.Close()

	memStart := uintptr(unsafe.Pointer(&(*h.memoryArea)[0]))

	// Track the final expected state per offset by replaying operations in order.
	expected := make(map[uint]pageExpectation)

	for _, op := range operations {
		off := uint(op.offset)
		switch op.mode {
		case operationModeRead:
			if _, seen := expected[off]; !seen {
				expected[off] = expectClean
			}
		case operationModeWrite:
			expected[off] = expectDirty
		}
	}

	for off, expect := range expected {
		entry, err := pagemap.ReadEntry(memStart + uintptr(off))
		require.NoError(t, err, "pagemap read at offset %d", off)

		switch expect {
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
