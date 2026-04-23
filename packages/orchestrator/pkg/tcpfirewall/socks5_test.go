package tcpfirewall

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	xproxy "golang.org/x/net/proxy"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

// newTestSandbox mirrors the pattern from handlers_test.go so BYOP helpers
// operate on a realistic sbx shape without pulling in a full sandbox lifecycle.
func newTestSandbox(sandboxID string, egress *orchestrator.SandboxNetworkEgressConfig) *sandbox.Sandbox {
	return &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Runtime: sandbox.RuntimeMetadata{SandboxID: sandboxID},
			Config: sandbox.NewConfig(sandbox.Config{
				Network: &orchestrator.SandboxNetworkConfig{Egress: egress},
			}),
		},
	}
}

func TestSOCKS5AuthFromEgress_NoAuth(t *testing.T) {
	t.Parallel()

	auth := socks5AuthFromEgress(&orchestrator.SandboxNetworkEgressConfig{
		EgressProxyAddress: "proxy:1080",
	}, "i-abc123")

	assert.Nil(t, auth, "no user/pass must yield nil auth (SOCKS5 no-auth method)")
}

func TestSOCKS5AuthFromEgress_UserPass(t *testing.T) {
	t.Parallel()

	auth := socks5AuthFromEgress(&orchestrator.SandboxNetworkEgressConfig{
		EgressProxyUsername: "alice",
		EgressProxyPassword: "s3cret",
	}, "i-abc123")

	require.NotNil(t, auth)
	assert.Equal(t, "alice", auth.User)
	assert.Equal(t, "s3cret", auth.Password)
}

func TestSOCKS5AuthFromEgress_SubstitutesSandboxID(t *testing.T) {
	t.Parallel()

	auth := socks5AuthFromEgress(&orchestrator.SandboxNetworkEgressConfig{
		EgressProxyUsername: "sbx-{{sandboxID}}",
		EgressProxyPassword: "token-{{sandboxID}}-rest",
	}, "i-abc123")

	require.NotNil(t, auth)
	assert.Equal(t, "sbx-i-abc123", auth.User)
	assert.Equal(t, "token-i-abc123-rest", auth.Password)
}

func TestSOCKS5AuthFromEgress_MultiplePlaceholders(t *testing.T) {
	t.Parallel()

	// ReplaceAll must substitute every occurrence — users sometimes template
	// both halves of "{{sandboxID}}:{{sandboxID}}".
	auth := socks5AuthFromEgress(&orchestrator.SandboxNetworkEgressConfig{
		EgressProxyUsername: "{{sandboxID}}-{{sandboxID}}",
	}, "abc")

	require.NotNil(t, auth)
	assert.Equal(t, "abc-abc", auth.User)
}

func TestSOCKS5DialContextFromEgress_NoBYOP_ReturnsNil(t *testing.T) {
	t.Parallel()

	// Sandbox without a configured proxy: nil is the zero-overhead fast-path
	// that callers key off of.
	sbx := newTestSandbox("i-abc123", &orchestrator.SandboxNetworkEgressConfig{})
	assert.Nil(t, socks5DialContextFromEgress(sbx))

	// And nil egress at all.
	sbxNoEgress := newTestSandbox("i-abc123", nil)
	assert.Nil(t, socks5DialContextFromEgress(sbxNoEgress))
}

func TestSOCKS5DialContextFromEgress_WithBYOP_ReturnsDialer(t *testing.T) {
	t.Parallel()

	sbx := newTestSandbox("i-abc123", &orchestrator.SandboxNetworkEgressConfig{
		EgressProxyAddress: "203.0.113.5:1080",
	})
	dc := socks5DialContextFromEgress(sbx)
	require.NotNil(t, dc, "a non-empty proxy address must produce a dialer")
}

// withStubSOCKSResolver replaces the SOCKS5 host resolver for the duration of
// the test. Not parallel-safe; callers must not use t.Parallel() alongside.
func withStubSOCKSResolver(t *testing.T, table map[string][]net.IP) {
	t.Helper()
	orig := resolveSOCKS5Host
	resolveSOCKS5Host = func(_ context.Context, host string) ([]net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		if ips, ok := table[host]; ok {
			return ips, nil
		}
		return nil, errors.New("stub resolver: unknown host " + host)
	}
	t.Cleanup(func() { resolveSOCKS5Host = orig })
}

func TestValidateSOCKS5Endpoint_AcceptsPublicIP(t *testing.T) {
	require.NoError(t, validateSOCKS5Endpoint(context.Background(), "203.0.113.5:1080"))
}

func TestValidateSOCKS5Endpoint_RejectsInternalLiteral(t *testing.T) {
	err := validateSOCKS5Endpoint(context.Background(), "10.0.0.5:1080")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSOCKS5EndpointInternal,
		"literal internal IP must surface ErrSOCKS5EndpointInternal")
}

func TestValidateSOCKS5Endpoint_RejectsRebindToInternal(t *testing.T) {
	withStubSOCKSResolver(t, map[string][]net.IP{
		// DNS-rebind scenario: hostname was public at API validation time,
		// resolves to RFC1918 now.
		"rebind.example.com": {net.ParseIP("10.0.0.5")},
	})
	err := validateSOCKS5Endpoint(context.Background(), "rebind.example.com:1080")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSOCKS5EndpointInternal)
}

func TestValidateSOCKS5Endpoint_RejectsAnyInternalInMultiResolve(t *testing.T) {
	// One public + one private A record: must reject (fail-closed).
	withStubSOCKSResolver(t, map[string][]net.IP{
		"mixed.example.com": {net.ParseIP("203.0.113.5"), net.ParseIP("10.0.0.5")},
	})
	err := validateSOCKS5Endpoint(context.Background(), "mixed.example.com:1080")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSOCKS5EndpointInternal)
}

func TestValidateSOCKS5Endpoint_MalformedAddress(t *testing.T) {
	err := validateSOCKS5Endpoint(context.Background(), "no-port")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSOCKS5EndpointInternal,
		"malformed address must fail with a parse error, not a rebind error")
}

func TestNewSOCKS5DialContext_RejectsRebind(t *testing.T) {
	withStubSOCKSResolver(t, map[string][]net.IP{
		"rebind.example.com": {net.ParseIP("192.168.1.5")},
	})
	dc := newSOCKS5DialContext("rebind.example.com:1080", nil)

	_, err := dc(context.Background(), "tcp", "example.org:443")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSOCKS5EndpointInternal)
}

// Confirm the dialer returned by proxy.SOCKS5 implements ContextDialer in the
// vendored x/net version so the fast path is taken. Purely a compile-time
// sanity check; if x/net ever regresses this will help triage quickly.
func TestXProxySOCKS5ReturnsContextDialer(t *testing.T) {
	t.Parallel()
	d, err := xproxy.SOCKS5("tcp", "203.0.113.5:1080", nil, &net.Dialer{})
	require.NoError(t, err)
	_, ok := d.(xproxy.ContextDialer)
	assert.True(t, ok, "x/net/proxy.SOCKS5 must return a ContextDialer")
}

func TestSbxHasBYOP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		egress *orchestrator.SandboxNetworkEgressConfig
		want   bool
	}{
		{"nil egress", nil, false},
		{"empty address", &orchestrator.SandboxNetworkEgressConfig{}, false},
		{
			"proxy configured",
			&orchestrator.SandboxNetworkEgressConfig{EgressProxyAddress: "proxy:1080"},
			true,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			sbx := newTestSandbox("i-abc123", c.egress)
			assert.Equal(t, c.want, sbxHasBYOP(sbx))
		})
	}
}
