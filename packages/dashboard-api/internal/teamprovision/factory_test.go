package teamprovision

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProvisionSink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		baseURL  string
		apiToken string
		wantErr  error
		wantNoop bool
	}{
		{
			name:     "noop when billing is not configured",
			wantNoop: true,
		},
		{
			name:     "noop when billing secrets are whitespace",
			baseURL:  "  ",
			apiToken: "\t",
			wantNoop: true,
		},
		{
			name:     "http when billing secrets are configured",
			baseURL:  "https://billing.example.com",
			apiToken: "token",
		},
		{
			name:    "error when only url is configured",
			baseURL: "https://billing.example.com",
			wantErr: ErrMissingAPIToken,
		},
		{
			name:     "error when only token is configured",
			apiToken: "token",
			wantErr:  ErrMissingBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink, err := NewProvisionSink(t.Context(), tt.baseURL, tt.apiToken)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, sink)

				return
			}

			require.NoError(t, err)
			if tt.wantNoop {
				require.IsType(t, &NoopProvisionSink{}, sink)

				return
			}

			require.IsType(t, &HTTPProvisionSink{}, sink)
		})
	}
}
