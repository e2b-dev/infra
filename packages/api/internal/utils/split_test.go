package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShortID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "composite ID extracts sandbox part",
			input: "i1a2b3c4d5e6f7g8h9j0k-clientid123",
			want:  "i1a2b3c4d5e6f7g8h9j0k",
		},
		{
			name:  "bare sandbox ID passes through",
			input: "i1a2b3c4d5e6f7g8h9j0k",
			want:  "i1a2b3c4d5e6f7g8h9j0k",
		},
		{
			name:  "short valid ID",
			input: "abc123",
			want:  "abc123",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "contains colon (Redis separator)",
			input:   "abc:def",
			wantErr: true,
		},
		{
			name:    "contains braces (Redis hash slot)",
			input:   "abc{0}",
			wantErr: true,
		},
		{
			name:    "contains uppercase",
			input:   "abcDEF",
			wantErr: true,
		},
		{
			name:    "contains slash",
			input:   "abc/def",
			wantErr: true,
		},
		{
			name:    "composite with invalid sandbox part",
			input:   "abc:def-clientid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ShortID(tt.input)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
