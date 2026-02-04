package userfaultfd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestFree(t *testing.T) {
	t.Parallel()

	tests := []testConfig{
		{
			name:          "hugepage, dont need",
			pagesize:      header.HugepageSize,
			numberOfPages: 8,
			operations: []operation{
				{
					offset: 0,
					mode:   operationModeRead,
				},
				{
					offset: 0,
					mode:   operationModeDontNeed,
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
				case operationModeDontNeed:
					err := h.dontNeedMemory(t.Context(), operation)
					require.NoError(t, err, "for operation %+v", operation)
				}
			}

			expectedAccessedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)

			accessedOffsets, err := h.offsetsOnce()
			require.NoError(t, err)

			assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted")
		})
	}
}
