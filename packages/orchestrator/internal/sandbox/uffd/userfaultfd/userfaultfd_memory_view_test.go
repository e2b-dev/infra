package userfaultfd

// The tests for memory.View are reading memory from the same process the memory belongs to, but with the /proc/PID/mem file it should not matter.

import (
	"bytes"
	"os"
	"syscall"
	"testing"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

			expectedAccessedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)

			accessedOffsets, err := h.accessedOffsetsOnce()
			require.NoError(t, err)

			assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")

			view, err := memory.NewView(os.Getpid(), h.mapping)
			require.NoError(t, err)

			for _, operation := range tt.operations {
				readBytes := make([]byte, tt.pagesize)
				_, err = view.ReadAt(readBytes, operation.offset)
				require.NoError(t, err)

				expectedBytes, err := h.data.Slice(t.Context(), operation.offset, int64(tt.pagesize))
				require.NoError(t, err)

				if !bytes.Equal(expectedBytes, readBytes) {
					idx, want, got := testutils.FirstDifferentByte(expectedBytes, readBytes)
					assert.Fail(t, "content mismatch", "want %x, got %x at index %d", want, got, idx)
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

	accessedOffsets, err := h.accessedOffsetsOnce()
	assert.Empty(t, accessedOffsets, "checking which pages were faulted")

	dirtyOffsets, err := h.dirtyOffsetsOnce()
	assert.Empty(t, dirtyOffsets, "checking which pages were dirty")

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

			expectedAccessedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)

			accessedOffsets, err := h.accessedOffsetsOnce()
			require.NoError(t, err)

			assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")

			view, err := memory.NewView(os.Getpid(), h.mapping)
			require.NoError(t, err)

			for _, operation := range tt.operations {
				readBytes := make([]byte, tt.pagesize)
				_, err = view.ReadAt(readBytes, operation.offset)
				require.NoError(t, err)

				expectedBytes, err := h.data.Slice(t.Context(), operation.offset, int64(tt.pagesize))
				require.NoError(t, err)

				if !bytes.Equal(expectedBytes, readBytes) {
					idx, want, got := testutils.FirstDifferentByte(expectedBytes, readBytes)
					assert.Fail(t, "content mismatch", "want %x, got %x at index %d", want, got, idx)
				}
			}
		})
	}
}
