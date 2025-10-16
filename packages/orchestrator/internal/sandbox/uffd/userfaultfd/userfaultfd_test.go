package userfaultfd

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"syscall"
	"testing"

	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestUffdMissing(t *testing.T) {
	tests := []testConfig{
		{
			name:          "standard 4k page, operation at start",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeRead,
				},
			},
		},
		{
			name:          "standard 4k page, operation at middle",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 15 * header.PageSize,
					mode:   operationModeRead,
				},
			},
		},
		{
			name:          "standard 4k page, operation at last page",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 31 * header.PageSize,
					mode:   operationModeRead,
				},
			},
		},
		{
			name:          "hugepage, operation at start",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeRead,
				},
			},
		},
		{
			name:          "hugepage, operation at middle",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeRead,
				},
			},
		},
		{
			name:          "hugepage, operation at last page",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 7 * header.HugepageSize,
					mode:   operationModeRead,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := configureTest(t, tt)

			for _, operation := range tt.operations {
				if operation.mode == operationModeRead {
					err := h.executeRead(t.Context(), operation)
					require.NoError(t, err)
				}
			}

			memoryMapAccesses := getOperationsOffsets(tt.operations, operationModeRead)
			assert.Equal(t, memoryMapAccesses, h.memoryMap.Map(), "checking which pages were accessed")
		})
	}
}

func TestUffdWriteProtection(t *testing.T) {
	tests := []testConfig{
		{
			name:          "standard 4k page, single write",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "standard 4k page, single read then write on first page",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeRead,
				},
				{
					offset: 0,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "standard 4k page, single read then write on non-first page",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 15 * header.PageSize,
					mode:   operationModeRead,
				},
				{
					offset: 15 * header.PageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "standard 4k page, two writes on different pages",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 15 * header.PageSize,
					mode:   operationModeWrite,
				},
				{
					offset: 16 * header.PageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, single write",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, single read then write on first page",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeRead,
				},
				{
					offset: 0,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, single read then write on non-first page",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeRead,
				},
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, two writes on different pages",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeWrite,
				},
				{
					offset: 4 * header.HugepageSize,
					mode:   operationModeWrite,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := configureTest(t, tt)

			for _, operation := range tt.operations {
				if operation.mode == operationModeRead {
					err := h.executeRead(t.Context(), operation)
					require.NoError(t, err)
				}

				if operation.mode == operationModeWrite {
					err := h.executeWrite(t.Context(), operation)
					require.NoError(t, err)
				}
			}

			writeOperations := getOperationsOffsets(tt.operations, operationModeWrite)
			assert.Equal(t, writeOperations, getOffsetsFromBitset(h.dirty.BitSet(), tt.pagesize), "checking written to pages")

			memoryAccesses := getOperationsOffsets(tt.operations, 0)
			assert.Equal(t, memoryAccesses, h.memoryMap.Map(), "checking which pages were accessed (read and write)")
		})
	}
}

type operationMode uint

const (
	// We are using 0 to get all operations
	operationModeRead operationMode = iota + 1
	operationModeWrite
)

type operation struct {
	// Offset in bytes. Must be smaller than the (numberOfPages-1) * pagesize as it reads a page and it must be aligned to the pagesize from the testConfig.
	offset uint
	mode   operationMode
}

type testConfig struct {
	name          string
	pagesize      uint64
	numberOfPages uint64
	operations    []operation
}

type testHandler struct {
	writeWaitCounter *utils.WaitCounter
	memoryArea       *[]byte
	pagesize         uint64
	data             block.Slicer
	dirty            *memory.Tracker
	memoryMap        *testutils.ContiguousMap
}

// If mode is 0, we will return offsets for all operations.
func getOperationsOffsets(operations []operation, mode operationMode) map[uint64]struct{} {
	count := map[uint64]struct{}{}

	for _, operation := range operations {
		if operation.mode == mode || mode == 0 {
			count[uint64(operation.offset)] = struct{}{}
		}
	}

	return count
}

func getOffsetsFromBitset(bitset *bitset.BitSet, pagesize uint64) map[uint64]struct{} {
	count := map[uint64]struct{}{}

	for i, e := bitset.NextSet(0); e; i, e = bitset.NextSet(i + 1) {
		count[uint64(header.BlockOffset(int64(i), int64(pagesize)))] = struct{}{}
	}

	return count
}

func (h *testHandler) executeRead(ctx context.Context, op operation) error {
	readBytes := (*h.memoryArea)[op.offset : op.offset+uint(h.pagesize)]

	expectedBytes, err := h.data.Slice(ctx, int64(op.offset), int64(h.pagesize))
	if err != nil {
		return err
	}

	if !bytes.Equal(readBytes, expectedBytes) {
		idx, want, got := testutils.DiffByte(readBytes, expectedBytes)
		return fmt.Errorf("content mismatch: want %q, got %q at index %d", want, got, idx)
	}

	return nil
}

func (h *testHandler) executeWrite(ctx context.Context, op operation) error {
	bytesToWrite, err := h.data.Slice(ctx, int64(op.offset), int64(h.pagesize))
	if err != nil {
		return err
	}

	copy((*h.memoryArea)[op.offset:op.offset+uint(h.pagesize)], bytesToWrite)

	err = h.writeWaitCounter.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for write requests finish: %w", err)
	}

	if !h.dirty.Check(int64(op.offset)) {
		return fmt.Errorf("dirty bit not set for page at offset %d, all dirty offsets: %v", op.offset, slices.Collect(maps.Keys(getOffsetsFromBitset(h.dirty.BitSet(), h.pagesize))))
	}

	return nil
}

func configureTest(t *testing.T, tt testConfig) *testHandler {
	data := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(uint64(size), tt.pagesize)
	require.NoError(t, err)

	t.Cleanup(func() {
		unmap()
	})

	m := testutils.NewContiguousMap(memoryStart, uint64(size), tt.pagesize)

	logger := testutils.NewTestLogger(t)

	uffd, err := newUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, data, m, logger)
	require.NoError(t, err)

	t.Cleanup(func() {
		uffd.Close()
	})

	err = uffd.configureApi(tt.pagesize)
	require.NoError(t, err)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP)
	require.NoError(t, err)

	fdExit, err := fdexit.New()
	require.NoError(t, err)

	t.Cleanup(func() {
		fdExit.SignalExit()
		fdExit.Close()
	})

	exitUffd := make(chan struct{}, 1)

	go func() {
		err := uffd.Serve(t.Context(), fdExit)
		assert.NoError(t, err)

		exitUffd <- struct{}{}
	}()

	t.Cleanup(func() {
		signalExitErr := fdExit.SignalExit()
		require.NoError(t, signalExitErr)

		select {
		case <-exitUffd:
		case <-t.Context().Done():
			t.Log("context done before exit:", t.Context().Err())
		}
	})

	return &testHandler{
		memoryArea:       &memoryArea,
		memoryMap:        m,
		pagesize:         tt.pagesize,
		dirty:            uffd.dirty,
		data:             data,
		writeWaitCounter: &uffd.writeRequestCounter,
	}
}

// TODO: Test write protection
// TODO: Test write protection with missing
// TODO: Test async write protection (if we decide for it)
// TODO: Test write protection double registration (with missing) to simulate the FC situation
