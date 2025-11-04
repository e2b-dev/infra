package userfaultfd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"syscall"
	"testing"

	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
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
	memoryArea      *[]byte
	pagesize        uint64
	data            *testutils.MemorySlicer
	memoryMap       *memory.Mapping
	uffd            *Userfaultfd
	missingRequests *sync.Map
	writeMu         sync.Mutex
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

	uffd, err := newUserfaultfd(syscall.O_CLOEXEC|syscall.O_NONBLOCK, data, m, logger)
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

	return &testHandler{
		memoryArea:      &memoryArea,
		memoryMap:       m,
		pagesize:        tt.pagesize,
		data:            data,
		uffd:            uffd,
		missingRequests: &uffd.missingRequests,
	}, cleanup
}

func (h *testHandler) getAccessedOffsets() []uint {
	offsets := []uint{}

	h.missingRequests.Range(func(key, _ any) bool {
		offsets = append(offsets, uint(key.(int64)))
		fmt.Fprintf(os.Stderr, "offset: %d\n", key.(int64))

		return true
	})

	return offsets
}

//go:noinline
func touchRead(b []byte) {
	var dst [1]byte
	_ = copy(dst[:], b[:1]) // forces a real read → MISSING fault
}

func (h *testHandler) executeRead(ctx context.Context, op operation) error {
	readBytes := (*h.memoryArea)[op.offset : op.offset+int64(h.pagesize)]
	touchRead(readBytes)

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

	// An unprotected parallel write to map results in undefined behavior—here usually manifesting as total freeze of the test.
	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	n := copy((*h.memoryArea)[op.offset:op.offset+int64(h.pagesize)], bytesToWrite)
	if n != int(h.pagesize) {
		return fmt.Errorf("copy length mismatch: want %d, got %d", h.pagesize, n)
	}

	return nil
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
