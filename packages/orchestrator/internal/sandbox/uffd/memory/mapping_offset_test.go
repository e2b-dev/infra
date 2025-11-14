package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestMapping_GetOffset(t *testing.T) {
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
		name             string
		hostVirtAddr     uintptr
		expectedOffset   int64
		expectedPagesize uintptr
		expectError      error
	}{
		{
			name:             "valid address in first region",
			hostVirtAddr:     0x1500,
			expectedOffset:   0x5500, // 0x5000 + (0x1500 - 0x1000)
			expectedPagesize: 0x1000,
		},
		{
			name:             "valid address at start of first region",
			hostVirtAddr:     0x1000,
			expectedOffset:   0x5000,
			expectedPagesize: 0x1000,
		},
		{
			name:             "valid address at end-1 of first region",
			hostVirtAddr:     0x2FFF, // 0x1000 + 0x2000 - 1
			expectedOffset:   0x6FFF, // 0x5000 + (0x2FFF - 0x1000)
			expectedPagesize: 0x1000,
		},
		{
			name:             "valid address in second region",
			hostVirtAddr:     0x5500,
			expectedOffset:   0x8500, // 0x8000 + (0x5500 - 0x5000)
			expectedPagesize: 0x1000,
		},
		{
			name:             "valid address at start of second region",
			hostVirtAddr:     0x5000,
			expectedOffset:   0x8000,
			expectedPagesize: 0x1000,
		},
		{
			name:             "valid address at end-1 of second region",
			hostVirtAddr:     0x5FFF,
			expectedOffset:   0x8FFF, // 0x8000 + (0x5FFF - 0x5000)
			expectedPagesize: 0x1000,
		},
		{
			name:         "address before first region",
			hostVirtAddr: 0x500,
			expectError:  AddressNotFoundError{hostVirtAddr: 0x500},
		},
		{
			name:         "address after last region",
			hostVirtAddr: 0x7000,
			expectError:  AddressNotFoundError{hostVirtAddr: 0x7000},
		},
		{
			name:         "address in gap between regions",
			hostVirtAddr: 0x4000,
			expectError:  AddressNotFoundError{hostVirtAddr: 0x4000},
		},
		{
			name:         "address at exact end of first region (exclusive)",
			hostVirtAddr: 0x3000, // 0x1000 + 0x2000
			expectError:  AddressNotFoundError{hostVirtAddr: 0x3000},
		},
		{
			name:         "address at exact end of second region (exclusive)",
			hostVirtAddr: 0x6000, // 0x5000 + 0x1000
			expectError:  AddressNotFoundError{hostVirtAddr: 0x6000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			offset, pagesize, err := mapping.GetOffset(tt.hostVirtAddr)
			if tt.expectError != nil {
				require.ErrorIs(t, err, tt.expectError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedOffset, offset)
				assert.Equal(t, tt.expectedPagesize, pagesize)
			}
		})
	}
}

func TestMapping_EmptyRegions(t *testing.T) {
	t.Parallel()

	mapping := NewMapping([]Region{})

	// Test GetOffset with empty regions
	_, _, err := mapping.GetOffset(0x1000)
	require.ErrorIs(t, err, AddressNotFoundError{hostVirtAddr: 0x1000})
}

func TestMapping_BoundaryConditions(t *testing.T) {
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
	offset, pagesize, err := mapping.GetOffset(0x1000)
	require.NoError(t, err)
	assert.Equal(t, int64(0x5000), offset) // 0x5000 + (0x1000 - 0x1000)

	// Test just before end boundary (exclusive)
	offset, pagesize, err = mapping.GetOffset(0x2FFF) // 0x1000 + 0x2000 - 1
	require.NoError(t, err)
	assert.Equal(t, int64(0x5000+(0x2FFF-0x1000)), offset) // 0x6FFF
	assert.Equal(t, uintptr(header.PageSize), pagesize)

	// Test exact end boundary (should fail - exclusive)
	_, _, err = mapping.GetOffset(0x3000) // 0x1000 + 0x2000
	require.ErrorIs(t, err, AddressNotFoundError{hostVirtAddr: 0x3000})

	// Test below start boundary (should fail)
	_, _, err = mapping.GetOffset(0x0FFF) // 0x1000 - 0x1000
	require.ErrorIs(t, err, AddressNotFoundError{hostVirtAddr: 0x0FFF})

}

func TestMapping_SingleLargeRegion(t *testing.T) {
	t.Parallel()

	// Entire 64-bit address space region
	regions := []Region{
		{
			BaseHostVirtAddr: 0x0,
			Size:             ^uintptr(0), // Max uintptr
			Offset:           0x100,
			PageSize:         header.PageSize,
		},
	}
	mapping := NewMapping(regions)

	offset, pagesize, err := mapping.GetOffset(0xABCDEF)
	require.NoError(t, err)
	assert.Equal(t, int64(0x100+0xABCDEF), offset)
	assert.Equal(t, uintptr(header.PageSize), pagesize)
}

func TestMapping_ZeroSizeRegion(t *testing.T) {
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

	_, _, err := mapping.GetOffset(0x2000)
	require.ErrorIs(t, err, AddressNotFoundError{hostVirtAddr: 0x2000})
}

func TestMapping_MultipleRegionsSparse(t *testing.T) {
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
	offset, pagesize, err := mapping.GetOffset(0x100)
	require.NoError(t, err)
	assert.Equal(t, int64(0x1000), offset)
	assert.Equal(t, uintptr(header.PageSize), pagesize)

	// Should succeed for start of second region
	offset, pagesize, err = mapping.GetOffset(0x10000)
	require.NoError(t, err)
	assert.Equal(t, int64(0x2000), offset)
	assert.Equal(t, uintptr(header.PageSize), pagesize)

	// In gap
	_, _, err = mapping.GetOffset(0x5000)
	require.ErrorIs(t, err, AddressNotFoundError{hostVirtAddr: 0x5000})
}

// Additional test for hugepage page size
func TestMapping_HugepagePagesize(t *testing.T) {
	t.Parallel()

	const hugepageSize = 2 * 1024 * 1024 // 2MB
	regions := []Region{
		{
			BaseHostVirtAddr: 0x400000,
			Size:             hugepageSize,
			Offset:           0x800000,
			PageSize:         hugepageSize,
		},
	}
	mapping := NewMapping(regions)

	// Test valid address in region using hugepages
	offset, pagesize, err := mapping.GetOffset(0x401000)
	require.NoError(t, err)
	assert.Equal(t, int64(0x800000+(0x401000-0x400000)), offset)
	assert.Equal(t, uintptr(hugepageSize), pagesize)

	// Test start of region
	offset, pagesize, err = mapping.GetOffset(0x400000)
	require.NoError(t, err)
	assert.Equal(t, int64(0x800000), offset)
	assert.Equal(t, uintptr(hugepageSize), pagesize)

	// Test end of region (exclusive, should fail)
	_, _, err = mapping.GetOffset(0x400000 + uintptr(hugepageSize))
	require.ErrorIs(t, err, AddressNotFoundError{hostVirtAddr: 0x400000 + uintptr(hugepageSize)})
}
