package userfaultfd

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

			runMatrix(t, tt, func(t *testing.T, cfg testConfig) {
				t.Helper()

				h, err := configureCrossProcessTest(t.Context(), t, cfg)
				require.NoError(t, err)

				h.executeAll(t, cfg.operations)

				expectedAccessedOffsets := getOperationsOffsets(cfg.operations, operationModeRead|operationModeWrite)

				states, err := h.pageStates()
				require.NoError(t, err)

				assert.Equal(t, expectedAccessedOffsets, states.allAccessed(), "checking which pages were faulted")

				h.checkDirtiness(t, cfg.operations)
			})
		})
	}
}

//nolint:tparallel // matrix arms intentionally serial; see runMatrix doc.
func TestParallelMissingWrite(t *testing.T) {
	t.Parallel()

	parallelOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	runMatrix(t, tt, func(t *testing.T, cfg testConfig) {
		t.Helper()

		h, err := configureCrossProcessTest(t.Context(), t, cfg)
		require.NoError(t, err)

		writeOp := operation{offset: 0, mode: operationModeWrite}

		var verr errgroup.Group

		for range parallelOperations {
			verr.Go(func() error {
				return h.executeWrite(t.Context(), writeOp)
			})
		}

		err = verr.Wait()
		require.NoError(t, err)

		expectedAccessedOffsets := getOperationsOffsets([]operation{writeOp}, operationModeRead|operationModeWrite)

		states, err := h.pageStates()
		require.NoError(t, err)

		assert.Equal(t, expectedAccessedOffsets, states.allAccessed(), "checking which pages were faulted")
	})
}

//nolint:tparallel // matrix arms intentionally serial; see runMatrix doc.
func TestParallelMissingWriteWithPrefault(t *testing.T) {
	t.Parallel()

	parallelOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	runMatrix(t, tt, func(t *testing.T, cfg testConfig) {
		t.Helper()

		h, err := configureCrossProcessTest(t.Context(), t, cfg)
		require.NoError(t, err)

		writeOp := operation{offset: 0, mode: operationModeWrite}

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

		states, err := h.pageStates()
		require.NoError(t, err)

		assert.Equal(t, expectedAccessedOffsets, states.allAccessed(), "checking which pages were faulted")
	})
}

//nolint:tparallel // matrix arms intentionally serial; see runMatrix doc.
func TestSerialMissingWrite(t *testing.T) {
	t.Parallel()

	serialOperations := 1_000_000

	tt := testConfig{
		pagesize:      header.PageSize,
		numberOfPages: 2,
	}

	runMatrix(t, tt, func(t *testing.T, cfg testConfig) {
		t.Helper()

		h, err := configureCrossProcessTest(t.Context(), t, cfg)
		require.NoError(t, err)

		writeOp := operation{offset: 0, mode: operationModeWrite}

		for range serialOperations {
			err := h.executeWrite(t.Context(), writeOp)
			require.NoError(t, err)
		}

		expectedAccessedOffsets := getOperationsOffsets([]operation{writeOp}, operationModeRead|operationModeWrite)

		states, err := h.pageStates()
		require.NoError(t, err)

		assert.Equal(t, expectedAccessedOffsets, states.allAccessed(), "checking which pages were faulted")
	})
}
