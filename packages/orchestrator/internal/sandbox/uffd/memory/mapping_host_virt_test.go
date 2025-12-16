package memory

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestMapping_GetHostVirtRanges(t *testing.T) {
	t.Parallel()

	regions := []Region{
		{
			BaseHostVirtAddr: 0x1000,
			Size:             0x2000,
			Offset:           0x5000,
			PageSize:         header.PageSize,
		},
		{
			BaseHostVirtAddr: 0x5000,
			Size:             0x1000,
			Offset:           0x8000,
			PageSize:         header.PageSize,
		},
	}
	mapping := NewMapping(regions)

	tests := []struct {
		name           string
		offset         int64
		size           int64
		expectedRanges []block.Range
		expectError    error
		expectErrorAt  int64 // offset where error should occur
	}{
		{
			name:   "valid offset in first region, single byte",
			offset: 0x5500, // 0x5000 + (0x1500 - 0x1000)
			size:   0x1,
			expectedRanges: []block.Range{
				{Start: 0x1500, Size: 0x1}, // 0x1000 + (0x5500 - 0x5000)
			},
		},
		{
			name:   "valid offset at start of first region, full region size",
			offset: 0x5000,
			size:   0x2000,
			expectedRanges: []block.Range{
				{Start: 0x1000, Size: 0x2000}, // 0x1000 + (0x5000 - 0x5000)
			},
		},
		{
			name:   "valid offset near end of first region, single byte",
			offset: 0x6FFF, // 0x7000 - 1
			size:   0x1,
			expectedRanges: []block.Range{
				{Start: 0x2FFF, Size: 0x1}, // 0x1000 + (0x6FFF - 0x5000)
			},
		},
		{
			name:   "valid offset at start of second region, full region size",
			offset: 0x8000,
			size:   0x1000,
			expectedRanges: []block.Range{
				{Start: 0x5000, Size: 0x1000}, // 0x5000 + (0x8000 - 0x8000)
			},
		},
		{
			name:          "offset before first region",
			offset:        0x4000,
			size:          0x100,
			expectError:   OffsetNotFoundError{offset: 0x4000},
			expectErrorAt: 0x4000,
		},
		{
			name:          "offset after last region",
			offset:        0xA000,
			size:          0x100,
			expectError:   OffsetNotFoundError{offset: 0xA000},
			expectErrorAt: 0xA000,
		},
		{
			name:          "offset in gap between regions",
			offset:        0x7000,
			size:          0x100,
			expectError:   OffsetNotFoundError{offset: 0x7000},
			expectErrorAt: 0x7000,
		},
		{
			name:          "offset at exact end of first region (exclusive)",
			offset:        0x7000, // 0x5000 + 0x2000
			size:          0x100,
			expectError:   OffsetNotFoundError{offset: 0x7000},
			expectErrorAt: 0x7000,
		},
		{
			name:          "offset at exact end of second region (exclusive)",
			offset:        0x9000, // 0x8000 + 0x1000
			size:          0x100,
			expectError:   OffsetNotFoundError{offset: 0x9000},
			expectErrorAt: 0x9000,
		},
		{
			name:          "range spanning from first region into gap (should fail at gap)",
			offset:        0x6F00,
			size:          0x200, // extends to 0x7100, crossing gap at 0x7000
			expectError:   OffsetNotFoundError{offset: 0x7000},
			expectErrorAt: 0x7000,
		},
		{
			name:          "range spanning both regions (fails due to gap)",
			offset:        0x6F00,
			size:          0x1100, // from 0x6F00 to 0x8000, but gap at 0x7000
			expectError:   OffsetNotFoundError{offset: 0x7000},
			expectErrorAt: 0x7000,
		},
		{
			name:   "range within first region, partial",
			offset: 0x5500,
			size:   0x500, // 0x5500 to 0x5A00
			expectedRanges: []block.Range{
				{Start: 0x1500, Size: 0x500}, // 0x1000 + (0x5500 - 0x5000)
			},
		},
		{
			name:          "range from end of first region to start of second (fails at gap)",
			offset:        0x6FFF,
			size:          0x1001, // from 0x6FFF to 0x8000, crossing gap
			expectError:   OffsetNotFoundError{offset: 0x7000},
			expectErrorAt: 0x7000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ranges, err := mapping.GetHostVirtRanges(tt.offset, tt.size)
			if tt.expectError != nil {
				require.Error(t, err)
				var offsetErr OffsetNotFoundError
				require.ErrorAs(t, err, &offsetErr)
				assert.Equal(t, tt.expectErrorAt, offsetErr.offset)
				assert.Nil(t, ranges)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedRanges, ranges)
			}
		})
	}
}

func TestMapping_GetHostVirtRanges_EmptyRegions(t *testing.T) {
	t.Parallel()

	mapping := NewMapping([]Region{})

	// Test GetHostVirtRanges with empty regions
	_, err := mapping.GetHostVirtRanges(0x1000, 0x100)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x1000})
}

func TestMapping_GetHostVirtRanges_BoundaryConditions(t *testing.T) {
	t.Parallel()

	regions := []Region{
		{
			BaseHostVirtAddr: 0x1000,
			Size:             0x2000,
			Offset:           0x5000,
			PageSize:         header.PageSize,
		},
	}

	mapping := NewMapping(regions)

	// Test exact start boundary
	ranges, err := mapping.GetHostVirtRanges(0x5000, 0x2000)
	require.NoError(t, err)
	assert.Equal(t, []block.Range{{Start: 0x1000, Size: 0x2000}}, ranges)

	// Test offset before end boundary
	ranges, err = mapping.GetHostVirtRanges(0x6FFF, 0x1) // just before end
	require.NoError(t, err)
	assert.Equal(t, []block.Range{{Start: 0x2FFF, Size: 0x1}}, ranges)

	// Test exact end boundary (should fail - exclusive)
	_, err = mapping.GetHostVirtRanges(0x7000, 0x100)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x7000})

	// Test below start boundary (should fail)
	_, err = mapping.GetHostVirtRanges(0x4000, 0x100)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x4000})
}

func TestMapping_GetHostVirtRanges_SingleLargeRegion(t *testing.T) {
	t.Parallel()

	// Entire 64-bit address space region
	regions := []Region{
		{
			BaseHostVirtAddr: 0x0,
			Size:             math.MaxInt64 - 0x100,
			Offset:           0x100,
			PageSize:         header.PageSize,
		},
	}
	mapping := NewMapping(regions)

	ranges, err := mapping.GetHostVirtRanges(0x100+0x1000, 0x1000) // Offset 0x1100, size 0x1000
	require.NoError(t, err)
	assert.Equal(t, []block.Range{{Start: 0x1000, Size: 0x1000}}, ranges)
}

func TestMapping_GetHostVirtRanges_ZeroSizeRegion(t *testing.T) {
	t.Parallel()

	regions := []Region{
		{
			BaseHostVirtAddr: 0x2000,
			Size:             0,
			Offset:           0x1000,
			PageSize:         header.PageSize,
		},
	}

	mapping := NewMapping(regions)

	_, err := mapping.GetHostVirtRanges(0x1000, 0x100)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x1000})
}

func TestMapping_GetHostVirtRanges_MultipleRegionsSparse(t *testing.T) {
	t.Parallel()

	regions := []Region{
		{
			BaseHostVirtAddr: 0x100,
			Size:             0x100,
			Offset:           0x1000,
			PageSize:         header.PageSize,
		},
		{
			BaseHostVirtAddr: 0x10000,
			Size:             0x100,
			Offset:           0x2000,
			PageSize:         header.PageSize,
		},
	}
	mapping := NewMapping(regions)

	// Should succeed for start of first region
	ranges, err := mapping.GetHostVirtRanges(0x1000, 0x100)
	require.NoError(t, err)
	assert.Equal(t, []block.Range{{Start: 0x100, Size: 0x100}}, ranges)

	// Should succeed for just before end of first region
	ranges, err = mapping.GetHostVirtRanges(0x10FF, 0x1) // 0x1100 - 1
	require.NoError(t, err)
	assert.Equal(t, []block.Range{{Start: 0x1FF, Size: 0x1}}, ranges)

	// Should succeed for start of second region
	ranges, err = mapping.GetHostVirtRanges(0x2000, 0x100)
	require.NoError(t, err)
	assert.Equal(t, []block.Range{{Start: 0x10000, Size: 0x100}}, ranges)

	// In gap
	_, err = mapping.GetHostVirtRanges(0x1500, 0x100)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x1500})
}
