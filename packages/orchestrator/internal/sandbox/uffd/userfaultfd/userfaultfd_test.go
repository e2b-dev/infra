package userfaultfd

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"syscall"
	"testing"

	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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

			err := h.uffd.writesInProgress.Wait(t.Context())
			require.NoError(t, err)

			expectedReadOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)
			assert.Equal(t, expectedReadOffsets, h.getReadOffsets(), "checking which pages were faulted)")
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
			name:          "standard 4k page, single read then write on first page (MISSING then WP)",
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
			name:          "standard 4k page, single write then read on first page (WRITE then skipping)",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeWrite,
				},
				{
					offset: 0,
					mode:   operationModeRead,
				},
			},
		},
		{
			name:          "standard 4k page, single read then write on non-first page (MISSING then WP)",
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
			name:          "standard 4k page, two writes on same page (WRITE then skipping)",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeWrite,
				},
				{
					offset: 0,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "standard 4k page, three writes on same page (WRITE then skipping)",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeWrite,
				},
				{
					offset: 0,
					mode:   operationModeWrite,
				},
				{
					offset: 0,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "standard 4k page, read then two writes on same page (MISSING then WP then WP)",
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
				{
					offset: 0,
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
			name:          "hugepage, single read then write on first page (MISSING then WP)",
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
			name:          "hugepage, single read then write on non-first page (MISSING then WP)",
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
			name:          "hugepage, single write then read on non-first page (WRITE then skipping)",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeWrite,
				},
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeRead,
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
		{
			name:          "hugepage, two writes on same page (WRITE then skipping)",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 0 * header.HugepageSize,
					mode:   operationModeWrite,
				},
				{
					offset: 0 * header.HugepageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, three writes on same page (WRITE then skipping)",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 0 * header.HugepageSize,
					mode:   operationModeWrite,
				},
				{
					offset: 0 * header.HugepageSize,
					mode:   operationModeWrite,
				},
				{
					offset: 0 * header.HugepageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, read then two writes on same page (MISSING then WP then WP)",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 0 * header.HugepageSize,
					mode:   operationModeRead,
				},
				{
					offset: 0 * header.HugepageSize,
					mode:   operationModeWrite,
				},
				{
					offset: 0 * header.HugepageSize,
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

			err := h.uffd.writesInProgress.Wait(t.Context())
			require.NoError(t, err)

			expectedWriteOffsets := getOperationsOffsets(tt.operations, operationModeWrite)
			assert.Equal(t, expectedWriteOffsets, h.getWriteOffsets(), "checking which pages were written to")

			expectedReadOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)
			assert.Equal(t, expectedReadOffsets, h.getReadOffsets(), "checking which pages were faulted)")
		})
	}
}

type testHandler struct {
	memoryArea *[]byte
	pagesize   uint64
	data       *testutils.MemorySlicer
	memoryMap  *memory.Mapping
	uffd       *Userfaultfd
}

func (h *testHandler) getReadOffsets() []uint {
	return utils.Map(slices.Collect(h.uffd.missingRequests.BitSet().Union(h.uffd.writeRequests.BitSet()).EachSet()), func(offset uint) uint {
		return uint(header.BlockOffset(int64(offset), int64(h.pagesize)))
	})
}

func (h *testHandler) getWriteOffsets() []uint {
	return utils.Map(slices.Collect(h.uffd.dirty.BitSet().EachSet()), func(offset uint) uint {
		return uint(header.BlockOffset(int64(offset), int64(h.pagesize)))
	})
}

func (h *testHandler) executeRead(ctx context.Context, op operation) error {
	readBytes := (*h.memoryArea)[op.offset : op.offset+int64(h.pagesize)]

	expectedBytes, err := h.data.Slice(ctx, op.offset, int64(h.pagesize))
	if err != nil {
		return err
	}

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

	n := copy((*h.memoryArea)[op.offset:op.offset+int64(h.pagesize)], bytesToWrite)
	if n != int(h.pagesize) {
		return fmt.Errorf("copy length mismatch: want %d, got %d", h.pagesize, n)
	}

	err = h.uffd.writesInProgress.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for write requests finish: %w", err)
	}

	if !h.uffd.dirty.Has(op.offset) {
		return fmt.Errorf("dirty bit not set for page at offset %d, all dirty offsets: %v", op.offset, h.getWriteOffsets())
	}

	return nil
}

func configureTest(t *testing.T, tt testConfig) *testHandler {
	t.Helper()

	data := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(uint64(size), tt.pagesize)
	require.NoError(t, err)

	t.Cleanup(func() {
		unmap()
	})

	m := memory.NewMapping([]memory.Region{
		{
			BaseHostVirtAddr: memoryStart,
			Size:             uintptr(size),
			Offset:           uintptr(0),
			PageSize:         uintptr(tt.pagesize),
		},
	})

	logger := testutils.NewTestLogger(t)

	fdExit, err := fdexit.New()
	require.NoError(t, err)

	t.Cleanup(func() {
		fdExit.Close()
	})

	uffd, err := newUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, data, int64(tt.pagesize), m, logger)
	require.NoError(t, err)

	t.Cleanup(func() {
		uffd.Close()
	})

	err = uffd.configureApi(tt.pagesize)
	require.NoError(t, err)

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP)
	require.NoError(t, err)

	exitUffd := make(chan struct{}, 1)

	go func() {
		err := uffd.Serve(t.Context(), fdExit)
		assert.NoError(t, err)

		exitUffd <- struct{}{}
	}()

	t.Cleanup(func() {
		signalExitErr := fdExit.SignalExit()
		assert.NoError(t, signalExitErr)

		<-exitUffd
	})

	return &testHandler{
		memoryArea: &memoryArea,
		memoryMap:  m,
		pagesize:   tt.pagesize,
		data:       data,
		uffd:       uffd,
	}
}

// Get a bitset of the offsets of the operations for the given mode.
func getOperationsOffsets(ops []operation, m operationMode) []uint {
	b := bitset.New(0)

	for _, operation := range ops {
		if operation.mode&m != 0 {
			b.Set(uint(operation.offset))
		}
	}

	return slices.Collect(b.EachSet())
}

// TODO: Test write protection double registration (with missing) to simulate the FC situation
