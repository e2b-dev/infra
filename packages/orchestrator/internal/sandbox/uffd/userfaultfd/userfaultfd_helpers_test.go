package userfaultfd

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
)

type testConfig struct {
	name string
	// Page size of the memory area.
	pagesize uint64
	// Number of pages in the memory area.
	numberOfPages uint64
	// Operations to trigger on the memory area.
	operations []operation
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

type testHandler struct {
	memoryArea *[]byte
	pagesize   uint64
	data       *testutils.MemorySlicer
	// Returns offsets of the pages that were faulted.
	// It can only be called once.
	// Sorted in ascending order.
	accessedOffsetsOnce func() ([]uint, error)
	// Returns offsets of the pages that were dirtied.
	// It can only be called once.
	// Sorted in ascending order.
	dirtyOffsetsOnce func() ([]uint, error)

	mutex   sync.Mutex
	mapping *memory.Mapping
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
		return fmt.Errorf("content mismatch: %w", testutils.ErrorFromByteSlicesDifference(expectedBytes, readBytes))
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

// Get a bitset of the offsets of the operations for the given mode.
// Sorted in ascending order.
func getOperationsOffsets(ops []operation, m operationMode) []uint {
	b := bitset.New(0)

	for _, operation := range ops {
		if operation.mode&m != 0 {
			b.Set(uint(operation.offset))
		}
	}

	return slices.Collect(b.EachSet())
}

func accessed(u *Userfaultfd) *block.Tracker {
	u.settleRequests.Lock()
	defer u.settleRequests.Unlock()

	return u.missingRequests.Clone()
}
