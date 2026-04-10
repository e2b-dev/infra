package tcpfirewall

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	xproxy "golang.org/x/net/proxy"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func strPtr(s string) *string { return &s }

func TestSocks5AuthFromEgress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		egress   *orchestrator.SandboxNetworkEgressConfig
		expected *xproxy.Auth
	}{
		{
			name:     "no proxy fields",
			egress:   &orchestrator.SandboxNetworkEgressConfig{},
			expected: nil,
		},
		{
			name: "address only, no auth",
			egress: &orchestrator.SandboxNetworkEgressConfig{
				EgressProxyAddress: strPtr("proxy.example.com:1080"),
			},
			expected: nil,
		},
		{
			name: "with username and password",
			egress: &orchestrator.SandboxNetworkEgressConfig{
				EgressProxyAddress:  strPtr("proxy.example.com:1080"),
				EgressProxyUsername: strPtr("user"),
				EgressProxyPassword: strPtr("pass"),
			},
			expected: &xproxy.Auth{User: "user", Password: "pass"},
		},
		{
			name: "username without password",
			egress: &orchestrator.SandboxNetworkEgressConfig{
				EgressProxyAddress:  strPtr("proxy.example.com:1080"),
				EgressProxyUsername: strPtr("user"),
			},
			expected: &xproxy.Auth{User: "user", Password: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := socks5AuthFromEgress(tt.egress)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.expected.User, result.User)
				assert.Equal(t, tt.expected.Password, result.Password)
			}
		})
	}
}

func TestNewSOCKS5DialContext(t *testing.T) {
	t.Parallel()

	t.Run("creates dialer without auth", func(t *testing.T) {
		t.Parallel()

		dialFn, err := newSOCKS5DialContext("127.0.0.1:1080", nil)
		require.NoError(t, err)
		assert.NotNil(t, dialFn)
	})

	t.Run("creates dialer with auth", func(t *testing.T) {
		t.Parallel()

		auth := &xproxy.Auth{User: "user", Password: "pass"}
		dialFn, err := newSOCKS5DialContext("127.0.0.1:1080", auth)
		require.NoError(t, err)
		assert.NotNil(t, dialFn)
	})
}
