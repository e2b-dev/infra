package discovery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func TestNewStaticFromAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		addr     string
		wantHost string
		wantPort uint16
		wantErr  bool
	}{
		{
			name:     "host with port",
			addr:     "127.0.0.1:5108",
			wantHost: "127.0.0.1",
			wantPort: 5108,
		},
		{
			name:     "dns host with port",
			addr:     "orchestrator.internal:5008",
			wantHost: "orchestrator.internal",
			wantPort: 5008,
		},
		{
			name:     "ipv6 host with port",
			addr:     "[::1]:5008",
			wantHost: "::1",
			wantPort: 5008,
		},
		{
			name:     "host without port defaults to the orchestrator API port",
			addr:     "127.0.0.1",
			wantHost: "127.0.0.1",
			wantPort: consts.OrchestratorAPIPort,
		},
		{
			name:    "empty address",
			addr:    "",
			wantErr: true,
		},
		{
			name:    "empty host",
			addr:    ":5008",
			wantErr: true,
		},
		{
			name:    "non-numeric port",
			addr:    "127.0.0.1:grpc",
			wantErr: true,
		},
		{
			name:    "port out of range",
			addr:    "127.0.0.1:70000",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sd, err := NewStaticFromAddress(tt.addr)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			items, err := sd.Query(context.Background())
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, tt.wantHost, items[0].LocalIPAddress)
			require.Equal(t, tt.wantPort, items[0].LocalInstanceApiPort)
		})
	}
}
