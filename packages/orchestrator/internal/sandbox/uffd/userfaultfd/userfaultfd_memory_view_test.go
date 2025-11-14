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
					assert.Fail(t, testutils.ErrorFromByteSlicesDifference(expectedBytes, readBytes).Error(), "for operation %+v", operation)
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

			view, err := memory.NewView(os.Getpid(), h.mapping)
			require.NoError(t, err)

			for _, op := range tt.operations {
				// An unprotected parallel write to map might result in an undefined behavior.
				h.mutex.Lock()

				data, err := writeData.Slice(t.Context(), op.offset, int64(h.pagesize))
				require.NoError(t, err)
				// We explicitly write to the memory area to make it differ from the default served content.
				n := copy((*h.memoryArea)[op.offset:op.offset+int64(h.pagesize)], data)
				h.mutex.Unlock()

				assert.Equal(t, int(h.pagesize), n, "copy length mismatch for operation %+v", op)

				readBytes := make([]byte, tt.pagesize)
				n, err = view.ReadAt(readBytes, op.offset)
				require.NoError(t, err)
				assert.Len(t, readBytes, n)

				expectedBytes, err := writeData.Slice(t.Context(), op.offset, int64(tt.pagesize))
				require.NoError(t, err)

				if !bytes.Equal(expectedBytes, readBytes) {
					assert.Fail(t, testutils.ErrorFromByteSlicesDifference(expectedBytes, readBytes).Error(), "for operation %+v", op)
				}
			}
		})
	}
}
