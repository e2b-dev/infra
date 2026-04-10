package teamprovision

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProvisionSink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		enabled  bool
		baseURL  string
		apiToken string
		wantErr  error
		wantType any
	}{
		{
			name:     "disabled returns noop",
			enabled:  false,
			wantType: &NoopProvisionSink{},
		},
		{
			name:     "enabled requires base url",
			enabled:  true,
			apiToken: "token",
			wantErr:  ErrMissingBaseURL,
		},
		{
			name:    "enabled requires api token",
			enabled: true,
			baseURL: "https://billing.example.com",
			wantErr: ErrMissingAPIToken,
		},
		{
			name:     "enabled returns http sink",
			enabled:  true,
			baseURL:  "https://billing.example.com",
			apiToken: "token",
			wantType: &HTTPProvisionSink{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink, err := NewProvisionSink(tt.enabled, tt.baseURL, tt.apiToken)
			if tt.wantErr != nil {
				require.Nil(t, sink)
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.IsType(t, tt.wantType, sink)
		})
	}
}
