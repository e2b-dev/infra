package filesystem

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFreeBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected int64
		wantErr  bool
	}{
		{
			name:     "standard debugfs output",
			input:    "Block count:              131072\nFree blocks:              120000\nFirst block:              0\n",
			expected: 120000,
		},
		{
			name:     "large block count",
			input:    "Free blocks:              999999999\n",
			expected: 999999999,
		},
		{
			name:    "missing free blocks",
			input:   "Block count:              131072\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := parseFreeBlocks(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestParseReservedBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected int64
		wantErr  bool
	}{
		{
			name:     "standard debugfs output",
			input:    "Block count:              131072\nReserved block count:     6553\nFree blocks:              120000\n",
			expected: 6553,
		},
		{
			name:     "zero reserved blocks",
			input:    "Reserved block count:     0\n",
			expected: 0,
		},
		{
			name:    "missing reserved blocks",
			input:   "Block count:              131072\nFree blocks:              120000\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := parseReservedBlocks(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}
