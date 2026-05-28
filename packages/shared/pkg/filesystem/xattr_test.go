package filesystem

import (
	"strings"
	"testing"
)

func TestValidateMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		metadata map[string]string
		wantErr  string
	}{
		{
			name:     "nil is ok",
			metadata: nil,
		},
		{
			name:     "empty is ok",
			metadata: map[string]string{},
		},
		{
			name:     "valid pair",
			metadata: map[string]string{"author": "mish"},
		},
		{
			name:     "empty key rejected",
			metadata: map[string]string{"": "v"},
			wantErr:  "must not be empty",
		},
		{
			name:     "oversized key rejected",
			metadata: map[string]string{strings.Repeat("k", MaxMetadataKeyLen+1): "v"},
			wantErr:  "exceeds",
		},
		{
			name:     "NUL in key rejected",
			metadata: map[string]string{"bad\x00key": "v"},
			wantErr:  "non-printable-ASCII",
		},
		{
			name:     "non-ASCII key rejected",
			metadata: map[string]string{"naïve": "v"},
			wantErr:  "non-printable-ASCII",
		},
		{
			name:     "non-ASCII value rejected",
			metadata: map[string]string{"k": "naïve"},
			wantErr:  "non-printable-ASCII",
		},
		{
			name:     "oversized value rejected",
			metadata: map[string]string{"k": strings.Repeat("v", MaxMetadataValueLen+1)},
			wantErr:  "value for key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateMetadata(tc.metadata)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
