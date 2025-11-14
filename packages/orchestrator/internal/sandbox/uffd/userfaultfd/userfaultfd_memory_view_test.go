package userfaultfd

// The tests for memory.View are reading memory from the same process the memory belongs to, but with the /proc/PID/mem file it should not matter.

import (
	"bytes"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestUffdMemoryViewFaulted(t *testing.T) {
	t.Parallel()

	tests := []testConfig{
		{
			name:          "standard 4k page, operation at start, read faulted",
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
			name:          "standard 4k page, operation at middle, read faulted",
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
			name:          "hugepage, operation at start, read faulted",
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
			name:          "hugepage, operation at middle, read faulted",
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
			name:          "standard 4k page, operation at start, write faulted",
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
			name:          "standard 4k page, operation at middle, write faulted",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 15 * header.PageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, operation at start, write faulted",
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
			name:          "hugepage, operation at middle, write faulted",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeWrite,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t, tt)
			require.NoError(t, err)

			for _, operation := range tt.operations {
				err := h.executeOperation(t.Context(), operation)
				assert.NoError(t, err, "for operation %+v", operation) //nolint:testifylint
			}

			view, err := memory.NewView(os.Getpid(), h.mapping)
			require.NoError(t, err)

			for _, operation := range tt.operations {
				readBytes := make([]byte, tt.pagesize)
				n, err := view.ReadAt(readBytes, operation.offset)
				require.NoError(t, err)
				assert.Len(t, readBytes, n)

				expectedBytes, err := h.data.Slice(t.Context(), operation.offset, int64(tt.pagesize))
				require.NoError(t, err)

				if !bytes.Equal(expectedBytes, readBytes) {
					idx, want, got := testutils.FirstDifferentByte(expectedBytes, readBytes)
					assert.Fail(t, "content mismatch", "want '%x', got '%x' at index %d, for operation %+v", want, got, idx, operation)
				}
			}
		})
	}
}

func TestUffdMemoryViewNotFaultedError(t *testing.T) {
	t.Parallel()

	test := testConfig{
		name:          "standard 4k page, operation at start",
		pagesize:      header.PageSize,
		numberOfPages: 32,
	}

	h, err := configureCrossProcessTest(t, test)
	require.NoError(t, err)

	view, err := memory.NewView(os.Getpid(), h.mapping)
	require.NoError(t, err)

	readBytes := make([]byte, header.PageSize)
	_, err = view.ReadAt(readBytes, 0)
	require.ErrorAs(t, err, &memory.MemoryNotFaultedError{})
	require.ErrorIs(t, err, syscall.EIO)
}

func TestUffdMemoryViewDirty(t *testing.T) {
	t.Parallel()

	tests := []testConfig{
		{
			name:          "standard 4k page, operation at start, write faulted",
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
			name:          "standard 4k page, operation at middle, write faulted",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 15 * header.PageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "standard 4k page, operation at end, write faulted",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 31 * header.PageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, operation at start, write faulted",
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
			name:          "hugepage, operation at middle, write faulted",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, operation at end, write faulted",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 3 * header.HugepageSize,
					mode:   operationModeWrite,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t, tt)
			require.NoError(t, err)

			writeData := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

			// For the first [pagesize] bytes write 1s, for the next [pagesize] bytes write 2s, etc.
			for i := range writeData.Content() {
				writeData.Content()[i] = byte((i / int(tt.pagesize)) + 1)
			}

			view, err := memory.NewView(os.Getpid(), h.mapping)
			require.NoError(t, err)

			for _, op := range tt.operations {
				// An unprotected parallel write to map might result in an undefined behavior.
				h.mutex.Lock()

				// We explicitly write to the memory area to make it differ from the default served content.
				n := copy((*h.memoryArea)[op.offset:op.offset+int64(h.pagesize)], writeData.Content())
				h.mutex.Unlock()

				if n != int(h.pagesize) {
					assert.Fail(t, "copy length mismatch", "want %d, got %d, for operation %+v", h.pagesize, n, op)
				}

				readBytes := make([]byte, tt.pagesize)
				n, err = view.ReadAt(readBytes, op.offset)
				require.NoError(t, err)
				assert.Len(t, readBytes, n)

				expectedBytes, err := writeData.Slice(t.Context(), op.offset, int64(tt.pagesize))
				require.NoError(t, err)

				if !bytes.Equal(expectedBytes, readBytes) {
					idx, want, got := testutils.FirstDifferentByte(expectedBytes, readBytes)
					assert.Fail(t, "content mismatch", "want '%x', got '%x' at index %d, for operation %+v", want, got, idx, op)
				}
			}
		})
	}
}
