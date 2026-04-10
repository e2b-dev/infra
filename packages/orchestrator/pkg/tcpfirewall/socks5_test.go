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
		name      string
		egress    *orchestrator.SandboxNetworkEgressConfig
		sandboxID string
		expected  *xproxy.Auth
	}{
		{
			name:      "no proxy fields",
			egress:    &orchestrator.SandboxNetworkEgressConfig{},
			sandboxID: "sbx-123",
			expected:  nil,
		},
		{
			name: "address only, no auth",
			egress: &orchestrator.SandboxNetworkEgressConfig{
				EgressProxyAddress: strPtr("proxy.example.com:1080"),
			},
			sandboxID: "sbx-123",
			expected:  nil,
		},
		{
			name: "with username and password",
			egress: &orchestrator.SandboxNetworkEgressConfig{
				EgressProxyUsername: strPtr("user"),
				EgressProxyPassword: strPtr("pass"),
			},
			sandboxID: "sbx-123",
			expected:  &xproxy.Auth{User: "user", Password: "pass"},
		},
		{
			name: "sandboxID placeholder in username",
			egress: &orchestrator.SandboxNetworkEgressConfig{
				EgressProxyUsername: strPtr("customer-session_{{sandboxID}}"),
				EgressProxyPassword: strPtr("token"),
			},
			sandboxID: "sbx-abc",
			expected:  &xproxy.Auth{User: "customer-session_sbx-abc", Password: "token"},
		},
		{
			name: "sandboxID placeholder in both",
			egress: &orchestrator.SandboxNetworkEgressConfig{
				EgressProxyUsername: strPtr("{{sandboxID}}"),
				EgressProxyPassword: strPtr("hmac-{{sandboxID}}"),
			},
			sandboxID: "sbx-xyz",
			expected:  &xproxy.Auth{User: "sbx-xyz", Password: "hmac-sbx-xyz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := socks5AuthFromEgress(tt.egress, tt.sandboxID)
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
