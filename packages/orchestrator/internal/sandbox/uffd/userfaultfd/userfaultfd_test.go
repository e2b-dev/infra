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
	return utils.Map(slices.Collect(h.uffd.missingRequests.BitSet().EachSet()), func(offset uint) uint {
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

	err = uffd.Register(memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING)
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
