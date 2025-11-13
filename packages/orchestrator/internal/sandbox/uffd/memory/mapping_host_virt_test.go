package memory

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestMapping_GetHostVirtAddr(t *testing.T) {
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
		name                string
		offset              int64
		expectedHostVirt    uintptr
		remainingRegionSize int64
		expectError         error
	}{
		{
			name:             "valid offset in first region",
			offset:           0x5500, // 0x5000 + (0x1500 - 0x1000)
			expectedHostVirt: 0x1500, // 0x1000 + (0x5500 - 0x5000)
			// region ends at 0x7000; remaining = 0x7000 - 0x5500 = 0x1b00
			remainingRegionSize: 0x1b00,
		},
		{
			name:                "valid offset at start of first region",
			offset:              0x5000,
			expectedHostVirt:    0x1000, // 0x1000 + (0x5000 - 0x5000)
			remainingRegionSize: 0x2000, // 0x7000 - 0x5000
		},
		{
			name:                "valid offset near end of first region",
			offset:              0x6FFF, // 0x7000 - 1
			expectedHostVirt:    0x2FFF, // 0x1000 + (0x6FFF - 0x5000)
			remainingRegionSize: 0x1,    // 0x7000 - 0x6FFF
		},
		{
			name:                "valid offset at start of second region",
			offset:              0x8000,
			expectedHostVirt:    0x5000, // 0x5000 + (0x8000 - 0x8000)
			remainingRegionSize: 0x1000, // 0x9000 - 0x8000
		},
		{
			name:        "offset before first region",
			offset:      0x4000,
			expectError: OffsetNotFoundError{offset: 0x4000},
		},
		{
			name:        "offset after last region",
			offset:      0xA000,
			expectError: OffsetNotFoundError{offset: 0xA000},
		},
		{
			name:        "offset in gap between regions",
			offset:      0x7000,
			expectError: OffsetNotFoundError{offset: 0x7000},
		},
		{
			name:        "offset at exact end of first region (exclusive)",
			offset:      0x7000, // 0x5000 + 0x2000
			expectError: OffsetNotFoundError{offset: 0x7000},
		},
		{
			name:        "offset at exact end of second region (exclusive)",
			offset:      0x9000, // 0x8000 + 0x1000
			expectError: OffsetNotFoundError{offset: 0x9000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostVirt, size, err := mapping.GetHostVirtAddr(tt.offset)
			if tt.expectError != nil {
				require.ErrorIs(t, err, tt.expectError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedHostVirt, hostVirt, "hostVirt: %d, expectedHostVirt: %d", hostVirt, tt.expectedHostVirt)
				assert.Equal(t, tt.remainingRegionSize, size, "size: %d, expectedSize: %d", size, tt.remainingRegionSize)
			}
		})
	}
}

func TestMapping_GetHostVirtAddr_EmptyRegions(t *testing.T) {
	mapping := NewMapping([]Region{})

	// Test GetHostVirtAddr with empty regions
	_, _, err := mapping.GetHostVirtAddr(0x1000)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x1000})
}

func TestMapping_GetHostVirtAddr_OverlappingRegions(t *testing.T) {
	// Test with overlapping regions (edge case)
	regions := []Region{
		{
			BaseHostVirtAddr: 0x1000,
			Size:             0x2000,
			Offset:           0x5000,
			PageSize:         header.PageSize,
		},
		{
			BaseHostVirtAddr: 0x2000, // Overlaps with first region
			Size:             0x1000,
			Offset:           0x8000,
			PageSize:         header.PageSize,
		},
	}

	mapping := NewMapping(regions)

	// The first matching region should be returned
	// Offset 0x6000 is in first region
	hostVirt, size, err := mapping.GetHostVirtAddr(0x6000)
	require.NoError(t, err)

	assert.Equal(t, uintptr(0x1000+(0x6000-0x5000)), hostVirt) // 0x2000
	// remainingRegionSize: endOffset (0x7000) - offset (0x6000) = 0x1000
	assert.Equal(t, int64(0x7000-0x6000), size)

	// Also test that the underlying implementation prefers the first region if both regions contain the offset
	hostVirt2, size2, err2 := mapping.GetHostVirtAddr(0x6000)
	require.NoError(t, err2)
	assert.Equal(t, uintptr(0x1000+(0x6000-0x5000)), hostVirt2) // 0x2000 from first region
	assert.Equal(t, int64(0x7000-0x6000), size2)
}

func TestMapping_GetHostVirtAddr_BoundaryConditions(t *testing.T) {
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
	hostVirt, size, err := mapping.GetHostVirtAddr(0x5000)
	require.NoError(t, err)
	assert.Equal(t, uintptr(0x1000), hostVirt)  // 0x1000 + (0x5000 - 0x5000)
	assert.Equal(t, int64(0x7000-0x5000), size) // 0x2000

	// Test offset before end boundary
	hostVirt, size, err = mapping.GetHostVirtAddr(0x6FFF) // just before end
	require.NoError(t, err)
	assert.Equal(t, uintptr(0x1000+(0x6FFF-0x5000)), hostVirt)
	assert.Equal(t, int64(0x7000-0x6FFF), size)

	// Test exact end boundary (should fail - exclusive)
	_, _, err = mapping.GetHostVirtAddr(0x7000)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x7000})

	// Test below start boundary (should fail)
	_, _, err = mapping.GetHostVirtAddr(0x4000)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x4000})
}

func TestMapping_GetHostVirtAddr_SingleLargeRegion(t *testing.T) {
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

	hostVirt, size, err := mapping.GetHostVirtAddr(0x100 + 0x1000) // Offset 0x1100
	require.NoError(t, err)
	assert.Equal(t, uintptr(0x1000), hostVirt) // 0x1000
	assert.Equal(t, int64(math.MaxInt64-0x100-0x1000), size)
}

func TestMapping_GetHostVirtAddr_ZeroSizeRegion(t *testing.T) {
	regions := []Region{
		{
			BaseHostVirtAddr: 0x2000,
			Size:             0,
			Offset:           0x1000,
			PageSize:         header.PageSize,
		},
	}

	mapping := NewMapping(regions)

	_, _, err := mapping.GetHostVirtAddr(0x1000)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x1000})
}

func TestMapping_GetHostVirtAddr_MultipleRegionsSparse(t *testing.T) {
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
	hostVirt, size, err := mapping.GetHostVirtAddr(0x1000)
	require.NoError(t, err)
	assert.Equal(t, uintptr(0x100), hostVirt)   // 0x100 + (0x1000 - 0x1000)
	assert.Equal(t, int64(0x1100-0x1000), size) // 0x100

	// Should succeed for just before end of first region
	hostVirt, size, err = mapping.GetHostVirtAddr(0x10FF) // 0x1100 - 1
	require.NoError(t, err)
	assert.Equal(t, uintptr(0x100+(0x10FF-0x1000)), hostVirt)
	assert.Equal(t, int64(0x1100-0x10FF), size) // 1

	// Should succeed for start of second region
	hostVirt, size, err = mapping.GetHostVirtAddr(0x2000)
	require.NoError(t, err)
	assert.Equal(t, uintptr(0x10000), hostVirt) // 0x10000 + (0x2000 - 0x2000)
	assert.Equal(t, int64(0x2100-0x2000), size) // 0x100

	// In gap
	_, _, err = mapping.GetHostVirtAddr(0x1500)
	require.ErrorIs(t, err, OffsetNotFoundError{offset: 0x1500})
}
