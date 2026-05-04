package jwtutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateAudienceMatchPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		policy    AudienceMatchPolicy
		audiences []string
		wantErr   string
	}{
		{
			name:      "empty audiences errors",
			policy:    "",
			audiences: nil,
			wantErr:   "audiences must contain at least one entry",
		},
		{
			name:      "single audience empty policy ok",
			policy:    "",
			audiences: []string{"a"},
		},
		{
			name:      "single audience MatchAny ok",
			policy:    AudienceMatchAny,
			audiences: []string{"a"},
		},
		{
			name:      "single audience invalid policy errors",
			policy:    "MatchAll",
			audiences: []string{"a"},
			wantErr:   "audienceMatchPolicy must be empty or",
		},
		{
			name:      "multiple audiences MatchAny ok",
			policy:    AudienceMatchAny,
			audiences: []string{"a", "b"},
		},
		{
			name:      "multiple audiences empty policy errors",
			policy:    "",
			audiences: []string{"a", "b"},
			wantErr:   "audienceMatchPolicy must be",
		},
		{
			name:      "multiple audiences invalid policy errors",
			policy:    "MatchAll",
			audiences: []string{"a", "b"},
			wantErr:   "audienceMatchPolicy must be",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateAudienceMatchPolicy(tt.policy, tt.audiences)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
