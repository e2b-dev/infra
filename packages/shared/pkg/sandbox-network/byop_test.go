package sandbox_network

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withStubResolver swaps resolveHost with a deterministic table for the
// duration of the test. Not parallel-safe.
func withStubResolver(t *testing.T, table map[string][]net.IP) {
	t.Helper()
	orig := resolveHost
	resolveHost = func(host string) ([]net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		ips, ok := table[host]
		if !ok {
			return nil, errors.New("stub resolver: unknown host " + host)
		}
		return ips, nil
	}
	t.Cleanup(func() { resolveHost = orig })
}

func TestValidateEgressProxy_NilPassthrough(t *testing.T) {
	got, err := ValidateEgressProxy(nil)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestValidateEgressProxy_HappyPath(t *testing.T) {
	withStubResolver(t, map[string][]net.IP{
		"proxy.example.com": {net.ParseIP("203.0.113.5")},
	})
	cfg := &EgressProxyConfig{
		Address:  "Proxy.Example.com:1080",
		Username: "alice",
		Password: "s3cret",
	}
	got, err := ValidateEgressProxy(cfg)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "proxy.example.com:1080", got.Address, "host should be lower-cased, port preserved")
	assert.Equal(t, "alice", got.Username)
	assert.Equal(t, "s3cret", got.Password)
}

func TestValidateEgressProxy_AcceptsIPLiteralWithoutResolving(t *testing.T) {
	// No stub resolver installed: IP literals must not require DNS.
	got, err := ValidateEgressProxy(&EgressProxyConfig{Address: "203.0.113.5:1080"})
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.5:1080", got.Address)
}

func TestValidateEgressProxy_RejectsMalformedAddress(t *testing.T) {
	cases := []struct {
		name  string
		addr  string
		match string
	}{
		{"empty", "", "must not be empty"},
		{"whitespace_only", "   ", "must not be empty"},
		{"no_port", "proxy.example.com", "host:port"},
		{"zero_port", "proxy.example.com:0", "valid 1-65535"},
		{"negative_port", "proxy.example.com:-1", "valid 1-65535"},
		{"out_of_range_port", "proxy.example.com:70000", "valid 1-65535"},
		{"non_numeric_port", "proxy.example.com:ssh", "valid 1-65535"},
		{"empty_host", ":1080", "host must not be empty"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, err := ValidateEgressProxy(&EgressProxyConfig{Address: c.addr})
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.match)
		})
	}
}

func TestValidateEgressProxy_RejectsInternalEndpoint(t *testing.T) {
	cases := []struct {
		name string
		addr string
		ips  []net.IP
	}{
		{"literal_loopback", "127.0.0.1:1080", nil},
		{"literal_ipv6_loopback", "[::1]:1080", nil},
		{"literal_private_10", "10.0.0.5:1080", nil},
		{"literal_private_172", "172.16.5.5:1080", nil},
		{"literal_private_192_168", "192.168.1.1:1080", nil},
		{"literal_link_local", "169.254.169.254:1080", nil},
		{"literal_ipv6_link_local", "[fe80::1]:1080", nil},
		{"resolves_to_private", "evil.example.com:1080", []net.IP{net.ParseIP("10.0.0.5")}},
		{
			"resolves_mixed_one_denied",
			"mixed.example.com:1080",
			[]net.IP{net.ParseIP("203.0.113.5"), net.ParseIP("10.0.0.5")},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if c.ips != nil {
				withStubResolver(t, map[string][]net.IP{"evil.example.com": c.ips, "mixed.example.com": c.ips})
			}
			_, err := ValidateEgressProxy(&EgressProxyConfig{Address: c.addr})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrEgressProxyInternalEndpoint,
				"expected ErrEgressProxyInternalEndpoint, got: %v", err)
		})
	}
}

func TestValidateEgressProxy_RejectsOrphanPassword(t *testing.T) {
	_, err := ValidateEgressProxy(&EgressProxyConfig{
		Address:  "203.0.113.5:1080",
		Username: "",
		Password: "somepw",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "password must be empty when username is empty")
}

func TestValidateEgressProxy_AcceptsEmptyCreds(t *testing.T) {
	got, err := ValidateEgressProxy(&EgressProxyConfig{Address: "203.0.113.5:1080"})
	require.NoError(t, err)
	assert.Empty(t, got.Username)
	assert.Empty(t, got.Password)
}

func TestValidateEgressProxy_PreservesSandboxIDPlaceholder(t *testing.T) {
	// {{sandboxID}} is allowed through unaltered; substitution happens at
	// dial time in the orchestrator.
	got, err := ValidateEgressProxy(&EgressProxyConfig{
		Address:  "203.0.113.5:1080",
		Username: "sbx-{{sandboxID}}",
		Password: "token-{{sandboxID}}",
	})
	require.NoError(t, err)
	assert.Equal(t, "sbx-{{sandboxID}}", got.Username)
	assert.Equal(t, "token-{{sandboxID}}", got.Password)
}

func TestValidateEgressProxy_DoesNotMutateInput(t *testing.T) {
	in := &EgressProxyConfig{
		Address:  "PROXY.EXAMPLE.COM:1080",
		Username: "u",
		Password: "p",
	}
	withStubResolver(t, map[string][]net.IP{
		"proxy.example.com": {net.ParseIP("203.0.113.5")},
	})
	_, err := ValidateEgressProxy(in)
	require.NoError(t, err)
	// Input preserved verbatim.
	assert.Equal(t, "PROXY.EXAMPLE.COM:1080", in.Address)
	assert.Equal(t, "u", in.Username)
	assert.Equal(t, "p", in.Password)
}

func TestRedactEgressProxy(t *testing.T) {
	t.Run("nil_returns_nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, RedactEgressProxy(nil))
	})
	t.Run("blanks_password_preserves_rest", func(t *testing.T) {
		t.Parallel()
		in := &EgressProxyConfig{
			Address:  "proxy.example.com:1080",
			Username: "u",
			Password: "hunter2",
		}
		out := RedactEgressProxy(in)
		require.NotNil(t, out)
		assert.Equal(t, in.Address, out.Address)
		assert.Equal(t, in.Username, out.Username)
		assert.Empty(t, out.Password)
		// Original not mutated.
		assert.Equal(t, "hunter2", in.Password)
	})
}

func TestIsIPInDeniedSandboxCIDRs_Exported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.5", true},
		{"172.16.0.1", true},
		{"192.168.5.5", true},
		{"127.0.0.1", true},
		{"169.254.169.254", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"8.8.8.8", false},
		{"203.0.113.5", false},
		{"2001:db8::1", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.ip, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, IsIPInDeniedSandboxCIDRs(net.ParseIP(c.ip)))
		})
	}
}
