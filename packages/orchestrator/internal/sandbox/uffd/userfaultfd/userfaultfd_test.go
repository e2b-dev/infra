package userfaultfd

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

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
			h, cleanupFunc := configureTest(t, tt)
			defer cleanupFunc()

			for _, operation := range tt.operations {
				if operation.mode == operationModeRead {
					err := h.executeRead(t.Context(), operation)
					require.NoError(t, err)
				}
			}

			err := h.uffd.writesInProgress.Wait(t.Context())
			require.NoError(t, err)

			expectedAccessedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)
			assert.Equal(t, expectedAccessedOffsets, h.getAccessedOffsets(), "checking which pages were faulted)")
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
			h, cleanup := configureTest(t, tt)
			t.Cleanup(cleanup)

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

			expectedAccessedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)
			assert.Equal(t, expectedAccessedOffsets, h.getAccessedOffsets(), "checking which pages were faulted)")
		})
	}
}

// Will trigger UFFD and with higher volume overload it before reaching our code.
func TestUffdParallelWP(t *testing.T) {
	parallelOperations := 10_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 5,
	}

	h, cleanup := configureTest(t, tt)
	t.Cleanup(cleanup)

	readOp := operation{
		offset: 0,
		mode:   operationModeRead,
	}

	// Single read to add Write protection to the page
	err := h.executeRead(t.Context(), readOp)
	require.NoError(t, err)

	writeOp := operation{
		offset: 0,
		mode:   operationModeWrite,
	}

	var verr errgroup.Group

	for range parallelOperations {
		verr.Go(func() error {
			return h.executeWrite(t.Context(), writeOp)
		})
	}

	err = verr.Wait()
	require.NoError(t, err)

	assert.Equal(t, []uint{0}, h.getAccessedOffsets(), "pages accessed (page 0)")
	assert.Equal(t, []uint{0}, h.getWriteOffsets(), "pages written to (page 0)")
}

// Will trigger UFFD and with higher volume overload it before reaching our code.
func TestUffdParallelWrite(t *testing.T) {
	parallelOperations := 10_00

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, cleanup := configureTest(t, tt)
	t.Cleanup(cleanup)

	writeOp := operation{
		offset: 0,
		mode:   operationModeWrite,
	}

	var verr errgroup.Group

	for range parallelOperations {
		verr.Go(func() error {
			return h.executeWrite(t.Context(), writeOp)
		})
	}

	err := verr.Wait()
	require.NoError(t, err)

	assert.Equal(t, []uint{0}, h.getAccessedOffsets(), "pages accessed (page 0)")
	assert.Equal(t, []uint{0}, h.getWriteOffsets(), "pages written to (page 0)")
}

func TestUffdParallelWriteWithPrefault(t *testing.T) {
	parallelOperations := 10_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, cleanup := configureTest(t, tt)
	t.Cleanup(cleanup)

	writeOp := operation{
		offset: 0,
		mode:   operationModeWrite,
	}

	err := h.executeWrite(t.Context(), writeOp)
	require.NoError(t, err)

	var verr errgroup.Group

	for range parallelOperations {
		verr.Go(func() error {
			return h.executeWrite(t.Context(), writeOp)
		})
	}

	err = verr.Wait()
	require.NoError(t, err)

	assert.Equal(t, []uint{0}, h.getAccessedOffsets(), "pages accessed (page 0)")
	assert.Equal(t, []uint{0}, h.getWriteOffsets(), "pages written to (page 0)")
}

func TestUffdParallelMissing(t *testing.T) {
	parallelOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, cleanup := configureTest(t, tt)
	t.Cleanup(cleanup)

	readOp := operation{
		offset: 0,
		mode:   operationModeRead,
	}

	var verr errgroup.Group

	for range parallelOperations {
		verr.Go(func() error {
			return h.executeRead(t.Context(), readOp)
		})
	}

	err := verr.Wait()
	require.NoError(t, err)

	assert.Equal(t, []uint{0}, h.getAccessedOffsets(), "pages accessed (page 0)")
}

func TestUffdParallelMissingWithPrefault(t *testing.T) {
	parallelOperations := 10_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, cleanup := configureTest(t, tt)
	t.Cleanup(cleanup)

	readOp := operation{
		offset: 0,
		mode:   operationModeRead,
	}

	err := h.executeRead(t.Context(), readOp)
	require.NoError(t, err)

	var verr errgroup.Group

	for range parallelOperations {
		verr.Go(func() error {
			return h.executeRead(t.Context(), readOp)
		})
	}

	err = verr.Wait()
	require.NoError(t, err)

	assert.Equal(t, []uint{0}, h.getAccessedOffsets(), "pages accessed (page 0)")
}

func TestUffdSerialWP(t *testing.T) {
	serialOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, cleanup := configureTest(t, tt)
	t.Cleanup(cleanup)

	readOp := operation{
		offset: 0,
		mode:   operationModeRead,
	}

	err := h.executeRead(t.Context(), readOp)
	require.NoError(t, err)

	writeOp := operation{
		offset: 0,
		mode:   operationModeWrite,
	}

	var verr errgroup.Group

	for range serialOperations {
		err = h.executeWrite(t.Context(), writeOp)
		require.NoError(t, err)
	}

	err = verr.Wait()
	require.NoError(t, err)

	assert.Equal(t, []uint{0}, h.getAccessedOffsets(), "pages accessed (page 0)")
}

func TestUffdSerialMissing(t *testing.T) {
	serialOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, cleanup := configureTest(t, tt)
	t.Cleanup(cleanup)

	readOp := operation{
		offset: 0,
		mode:   operationModeRead,
	}

	var verr errgroup.Group

	for range serialOperations {
		err := h.executeRead(t.Context(), readOp)
		require.NoError(t, err)
	}

	err := verr.Wait()
	require.NoError(t, err)

	assert.Equal(t, []uint{0}, h.getAccessedOffsets(), "pages accessed (page 0)")
}

type testHandler struct {
	memoryArea *[]byte
	pagesize   uint64
	data       *testutils.MemorySlicer
	memoryMap  *memory.Mapping
	uffd       *Userfaultfd
}

func (h *testHandler) getAccessedOffsets() []uint {
	return utils.Map(slices.Collect(h.uffd.missingRequests.BitSet().Union(h.uffd.dirty.BitSet()).EachSet()), func(offset uint) uint {
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

	// err = h.uffd.writesInProgress.Wait(ctx)
	// if err != nil {
	// 	return fmt.Errorf("failed to wait for write requests finish: %w", err)
	// }

	// if !h.uffd.dirty.Has(op.offset) {
	// 	return fmt.Errorf("dirty bit not set for page at offset %d, all dirty offsets: %v", op.offset, h.getWriteOffsets())
	// }

	return nil
}

func configureTest(t *testing.T, tt testConfig) (*testHandler, func()) {
	t.Helper()

	cleanupList := []func(){}

	cleanup := func() {
		slices.Reverse(cleanupList)

		for _, cleanup := range cleanupList {
			cleanup()
		}
	}

	data := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(uint64(size), tt.pagesize)
	require.NoError(t, err)

	cleanupList = append(cleanupList, func() {
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

	cleanupList = append(cleanupList, func() {
		fdExit.Close()
	})

	uffd, err := newUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, data, int64(tt.pagesize), m, logger)
	require.NoError(t, err)

	cleanupList = append(cleanupList, func() {
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

	cleanupList = append(cleanupList, func() {
		signalExitErr := fdExit.SignalExit()
		assert.NoError(t, signalExitErr)

		<-exitUffd
	})

	time.Sleep(1 * time.Second)

	return &testHandler{
		memoryArea: &memoryArea,
		memoryMap:  m,
		pagesize:   tt.pagesize,
		data:       data,
		uffd:       uffd,
	}, cleanup
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
