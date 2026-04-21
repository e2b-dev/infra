package userfaultfd

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
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
}

type operationMode uint32

const (
	operationModeRead operationMode = 1 << iota
	operationModeWrite
)

type operation struct {
	// Offset in bytes. Must be smaller than the (numberOfPages-1) * pagesize as it reads a page and it must be aligned to the pagesize from the testConfig.
	offset int64
	mode   operationMode
}

// handlerPageStates is a snapshot of the pageTracker grouped by state. It
// lets tests assert on the set of pages that the handler observed in each
// state, rather than a flat list of "accessed" offsets. Additional states
// (e.g. removed) are exposed so REMOVE-event tests can reuse this helper.
type handlerPageStates struct {
	faulted []uint
	removed []uint
}

// allAccessed returns the sorted union of offsets that the handler touched
// in any non-missing state. Tests that only care about "which pages did the
// handler see" can compare directly against this.
func (s handlerPageStates) allAccessed() []uint {
	b := bitset.New(0)
	for _, o := range s.faulted {
		b.Set(o)
	}
	for _, o := range s.removed {
		b.Set(o)
	}

	return slices.Collect(b.EachSet())
}

type testHandler struct {
	memoryArea *[]byte
	pagesize   uint64
	data       *MemorySlicer
	// pageStatesOnce returns a per-state snapshot of the handler's pageTracker.
	// It can only be called once.
	pageStatesOnce func() (handlerPageStates, error)
	mutex          sync.Mutex
}

func (h *testHandler) executeAll(t *testing.T, operations []operation) {
	t.Helper()

	for i, op := range operations {
		err := h.executeOperation(t.Context(), op)
		require.NoError(t, err, "step %d: %v at offset %d", i, op.mode, op.offset)
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
	default:
		return fmt.Errorf("invalid operation mode: %d", op.mode)
	}
}

func (h *testHandler) executeRead(ctx context.Context, op operation) error {
	readBytes := (*h.memoryArea)[op.offset : op.offset+int64(h.pagesize)]

	expectedBytes, err := h.data.Slice(ctx, op.offset, int64(h.pagesize))
	if err != nil {
		return err
	}

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
