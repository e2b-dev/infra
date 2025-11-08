package userfaultfd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

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

func TestUffdParallelWriteProtection(t *testing.T) {
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
