package uffd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestMissingWrite(t *testing.T) {
	t.Parallel()

	tests := []testConfig{
		{
			name:          "standard 4k page, operation at start",
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
			name:          "standard 4k page, operation at middle",
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
			name:          "standard 4k page, operation at last page",
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
			name:          "standard 4k page, read after write",
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
			name:          "hugepage, operation at start",
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
			name:          "hugepage, operation at middle",
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
			name:          "hugepage, operation at last page",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 7 * header.HugepageSize,
					mode:   operationModeWrite,
				},
			},
		},
		{
			name:          "hugepage, read after write",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t, tt)
			require.NoError(t, err)

			for _, operation := range tt.operations {
				if operation.mode == operationModeRead {
					err := h.executeRead(t.Context(), operation)
					require.NoError(t, err, "for operation %+v", operation)
				}

				if operation.mode == operationModeWrite {
					err := h.executeWrite(t.Context(), operation)
					require.NoError(t, err, "for operation %+v", operation)
				}
			}

			expectedAccessedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)

			accessedOffsets, err := h.accessed.Offsets(t.Context())
			require.NoError(t, err)

			assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
		})
	}
}

func TestParallelMissingWrite(t *testing.T) {
	t.Parallel()

	parallelOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, err := configureCrossProcessTest(t, tt)
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

	expectedAccessedOffsets := getOperationsOffsets([]operation{writeOp}, operationModeRead|operationModeWrite)

	accessedOffsets, err := h.accessed.Offsets(t.Context())
	require.NoError(t, err)

	assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
}

func TestParallelMissingWriteWithPrefault(t *testing.T) {
	t.Parallel()

	parallelOperations := 10_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, err := configureCrossProcessTest(t, tt)
	require.NoError(t, err)

	writeOp := operation{
		offset: 0,
		mode:   operationModeWrite,
	}

	err = h.executeWrite(t.Context(), writeOp)
	require.NoError(t, err)

	var verr errgroup.Group

	for range parallelOperations {
		verr.Go(func() error {
			return h.executeWrite(t.Context(), writeOp)
		})
	}

	err = verr.Wait()
	require.NoError(t, err)

	expectedAccessedOffsets := getOperationsOffsets([]operation{writeOp}, operationModeRead|operationModeWrite)

	accessedOffsets, err := h.accessed.Offsets(t.Context())
	require.NoError(t, err)

	assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
}

func TestSerialMissingWrite(t *testing.T) {
	t.Parallel()

	serialOperations := 10_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, err := configureCrossProcessTest(t, tt)
	require.NoError(t, err)

	writeOp := operation{
		offset: 0,
		mode:   operationModeWrite,
	}

	for range serialOperations {
		err := h.executeWrite(t.Context(), writeOp)
		require.NoError(t, err)
	}

	expectedAccessedOffsets := getOperationsOffsets([]operation{writeOp}, operationModeRead|operationModeWrite)

	accessedOffsets, err := h.accessed.Offsets(t.Context())
	require.NoError(t, err)

	assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
}
