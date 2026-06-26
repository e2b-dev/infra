package egress

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAllowlist(t *testing.T) {
	t.Parallel()

	t.Run("classifies CIDRs, bare IPs, domains, skips empties", func(t *testing.T) {
		t.Parallel()

		allow := ParseAllowlist([]string{"127.0.0.0/8", " 10.20.0.5 ", "", "192.168.5.0/24", "::1", "registry.corp.internal", "*.example.com", "*"})
		require.Len(t, allow.prefixes, 4)
		require.Equal(t, []string{"registry.corp.internal", "*.example.com", "*"}, allow.domains)

		assert.True(t, allow.prefixes[0].Contains(netip.MustParseAddr("127.5.5.5")))
		assert.True(t, allow.prefixes[1].Contains(netip.MustParseAddr("10.20.0.5")))
		assert.False(t, allow.prefixes[1].Contains(netip.MustParseAddr("10.20.0.6")))
		assert.True(t, allow.prefixes[2].Contains(netip.MustParseAddr("192.168.5.42")))
		assert.True(t, allow.prefixes[3].Contains(netip.MustParseAddr("::1")))
	})

	t.Run("empty input yields nothing", func(t *testing.T) {
		t.Parallel()

		allow := ParseAllowlist(nil)
		assert.Empty(t, allow.prefixes)
		assert.Empty(t, allow.domains)
	})

	t.Run("malformed CIDR is fail-safe (not a prefix)", func(t *testing.T) {
		t.Parallel()

		// "1.2.3.4/33" is an invalid prefix; it is kept as a never-matching
		// domain rather than becoming an IP prefix.
		allow := ParseAllowlist([]string{"10.0.0.0/8", "1.2.3.4/33"})
		require.Len(t, allow.prefixes, 1)
		assert.True(t, allow.prefixes[0].Contains(netip.MustParseAddr("10.1.2.3")))
		assert.Equal(t, []string{"1.2.3.4/33"}, allow.domains)
	})
}

func TestMatchDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		hostname string
		pattern  string
		want     bool
	}{
		{"registry.example.com", "registry.example.com", true},
		{"Registry.Example.com", "registry.example.com", true},
		{"registry.example.com", "*", true},
		{"a.corp.internal", "*.corp.internal", true},
		{"a.b.corp.internal", "*.corp.internal", true},
		{"evilcorp.internal", "*.corp.internal", false},
		{"corp.internal", "*.corp.internal", false},
		{"registry.example.com", "example.com", false},
		{"anything", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.hostname+"|"+tt.pattern, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, matchDomain(tt.hostname, tt.pattern))
		})
	}
}

func TestAllowlistPermitsIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		ip    string
		allow Allowlist
		want  bool
	}{
		{name: "public IP allowed with empty allowlist", ip: "1.1.1.1", want: true},
		{name: "private 10/8 blocked", ip: "10.0.0.5", want: false},
		{name: "private 192.168 blocked", ip: "192.168.1.1", want: false},
		{name: "cloud metadata blocked", ip: "169.254.169.254", want: false},
		{name: "loopback blocked", ip: "127.0.0.1", want: false},
		{name: "ipv6 loopback blocked", ip: "::1", want: false},
		{name: "ipv4-mapped private blocked", ip: "::ffff:10.0.0.1", want: false},
		{name: "loopback allowlisted via CIDR", ip: "127.0.0.1", allow: ParseAllowlist([]string{"127.0.0.0/8"}), want: true},
		{name: "single private IP allowlisted", ip: "10.20.0.5", allow: ParseAllowlist([]string{"10.20.0.5"}), want: true},
		{name: "non-listed private still blocked", ip: "10.20.0.6", allow: ParseAllowlist([]string{"10.20.0.5"}), want: false},
		{name: "allow-all disables guard", ip: "169.254.169.254", allow: ParseAllowlist([]string{"0.0.0.0/0"}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.allow.permitsIP(netip.MustParseAddr(tt.ip)))
		})
	}
}

func TestNewTransport(t *testing.T) {
	t.Parallel()

	// noProxyBase keeps the tests deterministic regardless of the runner's
	// HTTP_PROXY environment: with no proxy the guard is always installed.
	noProxyBase := func() *http.Transport { return &http.Transport{} }

	t.Run("blocks denied IP and records it", func(t *testing.T) {
		t.Parallel()

		var blocked netip.Addr
		tr := NewTransport(noProxyBase(), "registry.example.com", Allowlist{}, func(ip netip.Addr) { blocked = ip })

		_, err := tr.DialContext(t.Context(), "tcp", "10.0.0.5:443")
		require.ErrorIs(t, err, ErrBlocked)
		assert.Equal(t, "10.0.0.5", blocked.String())
	})

	t.Run("IP allowlist overrides denylist", func(t *testing.T) {
		t.Parallel()

		// Start a loopback listener; the guard must permit the dial because
		// 127.0.0.0/8 is allowlisted even though it is in the denylist.
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close()

		tr := NewTransport(noProxyBase(), "registry.example.com", ParseAllowlist([]string{"127.0.0.0/8"}), nil)
		conn, err := tr.DialContext(t.Context(), "tcp", ln.Addr().String())
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})

	t.Run("domain allowlist permits internal host by name", func(t *testing.T) {
		t.Parallel()

		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close()

		_, port, err := net.SplitHostPort(ln.Addr().String())
		require.NoError(t, err)

		// "localhost" resolves to loopback (denied) but is allowlisted by name,
		// so the dial must be permitted.
		tr := NewTransport(noProxyBase(), "registry.example.com", ParseAllowlist([]string{"localhost"}), nil)
		conn, err := tr.DialContext(t.Context(), "tcp", net.JoinHostPort("localhost", port))
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})

	t.Run("blocks loopback hostname when not allowlisted", func(t *testing.T) {
		t.Parallel()

		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close()

		_, port, err := net.SplitHostPort(ln.Addr().String())
		require.NoError(t, err)

		tr := NewTransport(noProxyBase(), "registry.example.com", Allowlist{}, nil)
		_, err = tr.DialContext(t.Context(), "tcp", net.JoinHostPort("localhost", port))
		require.ErrorIs(t, err, ErrBlocked)
	})

	t.Run("skips guard when a proxy is configured", func(t *testing.T) {
		t.Parallel()

		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close()

		// A configured proxy takes precedence: the guard must not be installed,
		// so an otherwise-denied IP is not blocked. The stub DialContext stands
		// in for the proxy connection.
		base := &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) {
				return url.Parse("http://proxy.internal:8080")
			},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
			},
		}

		tr := NewTransport(base, "registry.example.com", Allowlist{}, nil)
		conn, err := tr.DialContext(t.Context(), "tcp", "10.0.0.5:443")
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})

	t.Run("nil base falls back to a usable transport", func(t *testing.T) {
		t.Parallel()

		require.NotNil(t, NewTransport(nil, "registry.example.com", Allowlist{}, nil))
	})
}

func TestProxyConfigured(t *testing.T) {
	t.Parallel()

	// proxyUnless returns a proxy for every host except skip, simulating NO_PROXY.
	proxyUnless := func(skip string) func(*http.Request) (*url.URL, error) {
		return func(r *http.Request) (*url.URL, error) {
			if r.URL.Host == skip {
				return nil, nil
			}

			return url.Parse("http://proxy.internal:8080")
		}
	}

	tests := []struct {
		name  string
		proxy func(*http.Request) (*url.URL, error)
		host  string
		want  bool
	}{
		{name: "nil proxy func", proxy: nil, host: "registry.example.com", want: false},
		{
			name:  "func returns no proxy",
			proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
			host:  "registry.example.com",
			want:  false,
		},
		{
			name:  "func returns a proxy",
			proxy: func(*http.Request) (*url.URL, error) { return url.Parse("http://proxy.internal:8080") },
			host:  "registry.example.com",
			want:  true,
		},
		{name: "proxy applies to host", proxy: proxyUnless("direct.example.com"), host: "registry.example.com", want: true},
		{name: "NO_PROXY excludes host", proxy: proxyUnless("direct.example.com"), host: "direct.example.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, proxyConfigured(tt.proxy, tt.host))
		})
	}
}
