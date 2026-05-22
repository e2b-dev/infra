package discovery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIPAddressFromServiceHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		serviceHost string
		want        string
	}{
		{
			name:        "host with port",
			serviceHost: "10.0.0.12:5008",
			want:        "10.0.0.12",
		},
		{
			name:        "dns host with port",
			serviceHost: "orch-1.internal:5008",
			want:        "orch-1.internal",
		},
		{
			name:        "host without port",
			serviceHost: "10.0.0.12",
			want:        "10.0.0.12",
		},
		{
			name:        "ipv6 host with port",
			serviceHost: "[fd00::1]:5008",
			want:        "fd00::1",
		},
		{
			name:        "empty host",
			serviceHost: " ",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, ipAddressFromServiceHost(tt.serviceHost))
		})
	}
}
