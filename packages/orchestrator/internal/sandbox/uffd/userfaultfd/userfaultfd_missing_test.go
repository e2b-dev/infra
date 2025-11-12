package userfaultfd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestMissing(t *testing.T) {
	t.Parallel()

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
			name:          "standard 4k page, read after read",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeRead,
				},
				{
					offset: 0,
					mode:   operationModeRead,
				},
			},
		},
		{
			name:          "standard 4k page, reads, varying offsets",
			pagesize:      header.PageSize,
			numberOfPages: 32,
			operations: []operation{
				{
					offset: 4 * header.PageSize,
					mode:   operationModeRead,
				},
				{
					offset: 5 * header.PageSize,
					mode:   operationModeRead,
				},
				{
					offset: 2 * header.PageSize,
					mode:   operationModeRead,
				},
				{
					offset: 0 * header.PageSize,
					mode:   operationModeRead,
				},
				{
					offset: 0 * header.PageSize,
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
		{
			name:          "hugepage, read after read",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeRead,
				},
				{
					offset: 0,
					mode:   operationModeRead,
				},
			},
		},
		{
			name:          "hugepage, reads, varying offsets",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 4 * header.HugepageSize,
					mode:   operationModeRead,
				},
				{
					offset: 5 * header.HugepageSize,
					mode:   operationModeRead,
				},
				{
					offset: 2 * header.HugepageSize,
					mode:   operationModeRead,
				},
				{
					offset: 0 * header.HugepageSize,
					mode:   operationModeRead,
				},
				{
					offset: 0 * header.HugepageSize,
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
				switch operation.mode {
				case operationModeRead:
					err := h.executeRead(t.Context(), operation)
					require.NoError(t, err, "for operation %+v", operation)
				default:
					t.FailNow()
				}
			}

			expectedAccessedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)

			accessedOffsets, err := h.accessedOffsetsOnce()
			require.NoError(t, err)

			assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
		})
	}
}

func TestParallelMissing(t *testing.T) {
	t.Parallel()

	parallelOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, err := configureCrossProcessTest(t, tt)
	require.NoError(t, err)

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

	err = verr.Wait()
	require.NoError(t, err)

	expectedAccessedOffsets := getOperationsOffsets([]operation{readOp}, operationModeRead)

	accessedOffsets, err := h.accessedOffsetsOnce()
	require.NoError(t, err)

	assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
}

func TestParallelMissingWithPrefault(t *testing.T) {
	t.Parallel()

	parallelOperations := 10_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, err := configureCrossProcessTest(t, tt)
	require.NoError(t, err)

	readOp := operation{
		offset: 0,
		mode:   operationModeRead,
	}

	err = h.executeRead(t.Context(), readOp)
	require.NoError(t, err)

	var verr errgroup.Group

	for range parallelOperations {
		verr.Go(func() error {
			return h.executeRead(t.Context(), readOp)
		})
	}

	err = verr.Wait()
	require.NoError(t, err)

	expectedAccessedOffsets := getOperationsOffsets([]operation{readOp}, operationModeRead)

	accessedOffsets, err := h.accessedOffsetsOnce()
	require.NoError(t, err)

	assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
}

func TestSerialMissing(t *testing.T) {
	t.Parallel()

	serialOperations := 10_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, err := configureCrossProcessTest(t, tt)
	require.NoError(t, err)

	readOp := operation{
		offset: 0,
		mode:   operationModeRead,
	}

	for range serialOperations {
		err := h.executeRead(t.Context(), readOp)
		require.NoError(t, err)
	}

	expectedAccessedOffsets := getOperationsOffsets([]operation{readOp}, operationModeRead)

	accessedOffsets, err := h.accessedOffsetsOnce()
	require.NoError(t, err)

	assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
}

// TODO: Add mock Fd loop to test the ops separately from the serve loop
