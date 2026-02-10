package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseContentRangeTotal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected int64
	}{
		{"standard range", "bytes 0-99/12345", 12345},
		{"large object", "bytes 0-4194303/1073741824", 1073741824},
		{"mid-range request", "bytes 4194304-8388607/1073741824", 1073741824},
		{"single byte", "bytes 0-0/1", 1},
		{"no slash", "bytes 0-99", 0},
		{"empty string", "", 0},
		{"unknown total", "bytes 0-99/*", 0},
		{"trailing slash", "bytes 0-99/", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseContentRangeTotal(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
