package sandbox_network

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubResolver returns a HostResolver that resolves IP literals natively and
// looks every other host up in table. Unknown hosts produce an error so a
// test cannot accidentally hit real DNS.
func stubResolver(table map[string][]net.IP) HostResolver {
	return func(_ context.Context, host string) ([]net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		ips, ok := table[host]
		if !ok {
			return nil, errors.New("stub resolver: unknown host " + host)
		}

		return ips, nil
	}
}

// literalOnlyResolver is a resolver that resolves IP literals but errors on
// every hostname. Tests that should never hit DNS use it as a guard.
func literalOnlyResolver() HostResolver {
	return stubResolver(nil)
}

func TestValidateEgressProxy_NilPassthrough(t *testing.T) {
	t.Parallel()
	got, err := ValidateEgressProxy(t.Context(), nil, literalOnlyResolver())
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestValidateEgressProxy_HappyPath(t *testing.T) {
	t.Parallel()
	resolve := stubResolver(map[string][]net.IP{
		"proxy.example.com": {net.ParseIP("203.0.113.5")},
	})
	cfg := &EgressProxyConfig{
		Address:  "Proxy.Example.com:1080",
		Username: "alice",
		Password: "s3cret",
	}
	got, err := ValidateEgressProxy(t.Context(), cfg, resolve)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "proxy.example.com:1080", got.Address, "host should be lower-cased, port preserved")
	assert.Equal(t, "alice", got.Username)
	assert.Equal(t, "s3cret", got.Password)
}

func TestValidateEgressProxy_AcceptsIPLiteralWithoutResolving(t *testing.T) {
	t.Parallel()
	// IP literals must short-circuit before the resolver is invoked.
	got, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{Address: "203.0.113.5:1080"}, literalOnlyResolver())
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.5:1080", got.Address)
}

func TestValidateEgressProxy_RejectsMalformedAddress(t *testing.T) {
	t.Parallel()
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
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{Address: c.addr}, literalOnlyResolver())
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.match)
		})
	}
}

func TestValidateEgressProxy_RejectsInternalEndpoint(t *testing.T) {
	t.Parallel()
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
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			resolve := literalOnlyResolver()
			if c.ips != nil {
				resolve = stubResolver(map[string][]net.IP{
					"evil.example.com":  c.ips,
					"mixed.example.com": c.ips,
				})
			}
			_, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{Address: c.addr}, resolve)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrEgressProxyInternalEndpoint,
				"expected ErrEgressProxyInternalEndpoint, got: %v", err)
		})
	}
}

func TestValidateEgressProxy_RejectsOrphanPassword(t *testing.T) {
	t.Parallel()
	_, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{
		Address:  "203.0.113.5:1080",
		Username: "",
		Password: "somepw",
	}, literalOnlyResolver())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "password must be empty when username is empty")
}

func TestValidateEgressProxy_RejectsOverlongUsername(t *testing.T) {
	t.Parallel()
	_, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{
		Address:  "203.0.113.5:1080",
		Username: strings.Repeat("a", 256),
		Password: "pw",
	}, literalOnlyResolver())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username must not exceed 255 bytes")
}

func TestValidateEgressProxy_RejectsOverlongPassword(t *testing.T) {
	t.Parallel()
	_, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{
		Address:  "203.0.113.5:1080",
		Username: "alice",
		Password: strings.Repeat("p", 256),
	}, literalOnlyResolver())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "password must not exceed 255 bytes")
}

func TestValidateEgressProxy_AcceptsMaxLengthCreds(t *testing.T) {
	t.Parallel()
	user := strings.Repeat("a", 255)
	pass := strings.Repeat("p", 255)
	got, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{
		Address:  "203.0.113.5:1080",
		Username: user,
		Password: pass,
	}, literalOnlyResolver())
	require.NoError(t, err)
	assert.Equal(t, user, got.Username)
	assert.Equal(t, pass, got.Password)
}

func TestValidateEgressProxy_AcceptsEmptyCreds(t *testing.T) {
	t.Parallel()
	got, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{Address: "203.0.113.5:1080"}, literalOnlyResolver())
	require.NoError(t, err)
	assert.Empty(t, got.Username)
	assert.Empty(t, got.Password)
}

func TestValidateEgressProxy_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	in := &EgressProxyConfig{
		Address:  "PROXY.EXAMPLE.COM:1080",
		Username: "u",
		Password: "p",
	}
	resolve := stubResolver(map[string][]net.IP{
		"proxy.example.com": {net.ParseIP("203.0.113.5")},
	})
	_, err := ValidateEgressProxy(t.Context(), in, resolve)
	require.NoError(t, err)
	// Input preserved verbatim.
	assert.Equal(t, "PROXY.EXAMPLE.COM:1080", in.Address)
	assert.Equal(t, "u", in.Username)
	assert.Equal(t, "p", in.Password)
}

func TestValidateEgressProxy_NilResolverFallsBackToDefault(t *testing.T) {
	t.Parallel()
	// Passing nil resolve must not panic and must accept IP literals (the
	// default resolver short-circuits literals without touching DNS).
	got, err := ValidateEgressProxy(t.Context(), &EgressProxyConfig{Address: "203.0.113.5:1080"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.5:1080", got.Address)
}

func TestIsIPInDeniedSandboxCIDRs_Exported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.5", true},
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		{"172.16.0.1", true},
		{"192.168.5.5", true},
		{"127.0.0.1", true},
		{"169.254.169.254", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"1.0.0.1", false},
		{"8.8.8.8", false},
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		{"203.0.113.5", false},
		{"2001:db8::1", false},
		// The unspecified and "this network" block address are denied as BYOP endpoints.
		{"0.0.0.0", true},
		{"0.1.2.3", true},
		{"::", true},
	}
	for _, c := range cases {
		t.Run(c.ip, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, IsIPInDeniedSandboxCIDRs(net.ParseIP(c.ip)))
		})
	}
}
