package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsValidVolumeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		volume   string
		expected bool
	}{
		{
			name:     "valid name",
			volume:   "my-volume_123",
			expected: true,
		},
		{
			name:     "valid name with only numbers",
			volume:   "123456",
			expected: true,
		},
		{
			name:     "valid name with only letters",
			volume:   "myvolume",
			expected: true,
		},
		{
			name:     "valid name with hyphens",
			volume:   "my-volume",
			expected: true,
		},
		{
			name:     "valid name with underscores",
			volume:   "my_volume",
			expected: true,
		},
		{
			name:     "invalid name with space",
			volume:   "my volume",
			expected: false,
		},
		{
			name:     "invalid name with special character",
			volume:   "my-volume!",
			expected: false,
		},
		{
			name:     "invalid name with @",
			volume:   "my@volume",
			expected: false,
		},
		{
			name:     "empty name",
			volume:   "",
			expected: false,
		},
		{
			name:     "invalid name with leading dot",
			volume:   ".my-volume",
			expected: false,
		},
		{
			name:     "invalid name with trailing dot",
			volume:   "my-volume.",
			expected: false,
		},
		{
			name:     "invalid name with slash",
			volume:   "my/volume",
			expected: false,
		},
		{
			name:     "invalid name with backslash",
			volume:   "my\\volume",
			expected: false,
		},
		{
			name:     "invalid name with colon",
			volume:   "my:volume",
			expected: false,
		},
		{
			name:     "invalid name with asterisk",
			volume:   "my*volume",
			expected: false,
		},
		{
			name:     "invalid name with question mark",
			volume:   "my?volume",
			expected: false,
		},
		{
			name:     "invalid name with double quote",
			volume:   "my\"volume",
			expected: false,
		},
		{
			name:     "invalid name with less than",
			volume:   "my<volume",
			expected: false,
		},
		{
			name:     "invalid name with greater than",
			volume:   "my>volume",
			expected: false,
		},
		{
			name:     "invalid name with pipe",
			volume:   "my|volume",
			expected: false,
		},
		{
			name:     "invalid name with semicolon",
			volume:   "my;volume",
			expected: false,
		},
		{
			name:     "invalid name with comma",
			volume:   "my,volume",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			isValid := isValidVolumeName(tt.volume)
			assert.Equal(t, tt.expected, isValid)
		})
	}
}
