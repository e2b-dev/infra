package memory

import (
	"testing"
)

func TestMapping_GetOffset(t *testing.T) {
	regions := []Region{
		{
			BaseHostVirtAddr: 0x1000,
			Size:             0x2000,
			Offset:           0x5000,
			PageSize:         4096,
		},
		{
			BaseHostVirtAddr: 0x5000,
			Size:             0x1000,
			Offset:           0x8000,
			PageSize:         4096,
		},
	}
	mapping := NewMapping(regions)

	tests := []struct {
		name           string
		hostVirtAddr   uintptr
		expectedOffset int64
		expectedSize   uint64
		expectError    bool
	}{
		{
			name:           "valid address in first region",
			hostVirtAddr:   0x1500,
			expectedOffset: 0x5500, // 0x5000 + (0x1500 - 0x1000)
			expectedSize:   4096,
			expectError:    false,
		},
		{
			name:           "valid address in second region",
			hostVirtAddr:   0x5500,
			expectedOffset: 0x8500, // 0x8000 + (0x5500 - 0x5000)
			expectedSize:   4096,
			expectError:    false,
		},
		{
			name:         "address before first region",
			hostVirtAddr: 0x500,
			expectError:  true,
		},
		{
			name:         "address after last region",
			hostVirtAddr: 0x7000,
			expectError:  true,
		},
		{
			name:         "address in gap between regions",
			hostVirtAddr: 0x4000,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset, size, err := mapping.GetOffset(tt.hostVirtAddr)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if offset != tt.expectedOffset {
				t.Errorf("Expected offset %d, got %d", tt.expectedOffset, offset)
			}

			if size != tt.expectedSize {
				t.Errorf("Expected size %d, got %d", tt.expectedSize, size)
			}
		})
	}
}

func TestMapping_GetHostVirtAddr(t *testing.T) {
	regions := []Region{
		{
			BaseHostVirtAddr: 0x1000,
			Size:             0x2000,
			Offset:           0x5000,
			PageSize:         4096,
		},
		{
			BaseHostVirtAddr: 0x5000,
			Size:             0x1000,
			Offset:           0x8000,
			PageSize:         4096,
		},
	}
	mapping := NewMapping(regions)

	tests := []struct {
		name             string
		offset           int64
		expectedAddr     int64
		expectedPageSize uint64
		expectError      bool
	}{
		{
			name:             "valid offset in first region",
			offset:           0x5500,
			expectedAddr:     0x1500, // 0x1000 + (0x5500 - 0x5000)
			expectedPageSize: 4096,
			expectError:      false,
		},
		{
			name:             "valid offset in second region",
			offset:           0x8500,
			expectedAddr:     0x5500, // 0x5000 + (0x8500 - 0x8000)
			expectedPageSize: 4096,
			expectError:      false,
		},
		{
			name:        "offset before first region",
			offset:      0x4000,
			expectError: true,
		},
		{
			name:        "offset after last region",
			offset:      0x10000,
			expectError: true,
		},
		{
			name:        "offset in gap between regions",
			offset:      0x7000,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, pageSize, err := mapping.GetHostVirtAddr(tt.offset)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if addr != tt.expectedAddr {
				t.Errorf("Expected address %d, got %d", tt.expectedAddr, addr)
			}

			if pageSize != tt.expectedPageSize {
				t.Errorf("Expected page size %d, got %d", tt.expectedPageSize, pageSize)
			}
		})
	}
}

func TestMapping_EmptyRegions(t *testing.T) {
	mapping := NewMapping([]Region{})

	// Test GetOffset with empty regions
	_, _, err := mapping.GetOffset(0x1000)
	if err == nil {
		t.Errorf("Expected error for empty regions, got none")
	}

	// Test GetHostVirtAddr with empty regions
	_, _, err = mapping.GetHostVirtAddr(0x1000)
	if err == nil {
		t.Errorf("Expected error for empty regions, got none")
	}
}

func TestMapping_OverlappingRegions(t *testing.T) {
	// Test with overlapping regions (edge case)
	regions := []Region{
		{
			BaseHostVirtAddr: 0x1000,
			Size:             0x2000,
			Offset:           0x5000,
			PageSize:         4096,
		},
		{
			BaseHostVirtAddr: 0x2000, // Overlaps with first region
			Size:             0x1000,
			Offset:           0x8000,
			PageSize:         4096,
		},
	}
	mapping := NewMapping(regions)

	// The first matching region should be returned
	offset, _, err := mapping.GetOffset(0x2500) // In overlap area
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Should get result from first region
	expectedOffset := int64(0x5000 + (0x2500 - 0x1000)) // 0x6500
	if offset != expectedOffset {
		t.Errorf("Expected offset %d, got %d", expectedOffset, offset)
	}
}

func TestMapping_BoundaryConditions(t *testing.T) {
	regions := []Region{
		{
			BaseHostVirtAddr: 0x1000,
			Size:             0x2000,
			Offset:           0x5000,
			PageSize:         4096,
		},
	}
	mapping := NewMapping(regions)

	// Test exact start boundary
	offset, _, err := mapping.GetOffset(0x1000)
	if err != nil {
		t.Errorf("Unexpected error at start boundary: %v", err)
	}
	expectedOffset := int64(0x5000) // 0x5000 + (0x1000 - 0x1000)
	if offset != expectedOffset {
		t.Errorf("Expected offset %d at start boundary, got %d", expectedOffset, offset)
	}

	// Test just before end boundary (exclusive)
	offset, _, err = mapping.GetOffset(0x2FFF) // 0x1000 + 0x2000 - 1
	if err != nil {
		t.Errorf("Unexpected error just before end boundary: %v", err)
	}
	expectedOffset = int64(0x5000 + (0x2FFF - 0x1000)) // 0x6FFF
	if offset != expectedOffset {
		t.Errorf("Expected offset %d just before end boundary, got %d", expectedOffset, offset)
	}

	// Test exact end boundary (should fail - exclusive)
	_, _, err = mapping.GetOffset(0x3000) // 0x1000 + 0x2000
	if err == nil {
		t.Errorf("Expected error at end boundary (exclusive), got none")
	}
}
