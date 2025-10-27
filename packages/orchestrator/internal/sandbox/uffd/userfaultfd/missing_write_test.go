package userfaultfd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestMissingWrite(t *testing.T) {
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

				if operation.mode == operationModeWrite {
					err := h.executeWrite(t.Context(), operation)
					require.NoError(t, err)
				}
			}

			expectedAccessedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)
			assert.Equal(t, expectedAccessedOffsets, h.getAccessedOffsets(), "checking which pages were faulted")
		})
	}
}

func TestParallelMissingWrite(t *testing.T) {
	// TODO: At around 10k+ parallel operations the test often freezes.
	parallelOperations := 5_000

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

	expectedAccessedOffsets := getOperationsOffsets([]operation{writeOp}, operationModeRead|operationModeWrite)
	assert.Equal(t, expectedAccessedOffsets, h.getAccessedOffsets(), "checking which pages were faulted")
}

func TestParallelMissingWriteWithPrefault(t *testing.T) {
	parallelOperations := 1_000_000

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

	expectedAccessedOffsets := getOperationsOffsets([]operation{writeOp}, operationModeRead|operationModeWrite)
	assert.Equal(t, expectedAccessedOffsets, h.getAccessedOffsets(), "checking which pages were faulted")
}

func TestSerialMissingWrite(t *testing.T) {
	serialOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	h, cleanup := configureTest(t, tt)
	t.Cleanup(cleanup)

	writeOp := operation{
		offset: 0,
		mode:   operationModeRead,
	}

	var verr errgroup.Group

	for range serialOperations {
		err := h.executeWrite(t.Context(), writeOp)
		require.NoError(t, err)
	}

	err := verr.Wait()
	require.NoError(t, err)

	expectedAccessedOffsets := getOperationsOffsets([]operation{writeOp}, operationModeRead|operationModeWrite)
	assert.Equal(t, expectedAccessedOffsets, h.getAccessedOffsets(), "checking which pages were faulted")
}
