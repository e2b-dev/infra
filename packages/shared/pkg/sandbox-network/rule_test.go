package sandbox_network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		wantHost   string
		wantPort   [2]uint16 // {start, end}
		wantDomain bool
		wantCIDR   string // expected IPNet.String(), empty for domains
		wantErr    bool
		errSubstr  string
	}{
		// Backwards-compatible: bare host, all ports
		{
			name:     "bare IP",
			input:    "8.8.8.8",
			wantHost: "8.8.8.8", wantCIDR: "8.8.8.8/32",
		},
		{
			name:     "bare CIDR",
			input:    "10.0.0.0/8",
			wantHost: "10.0.0.0/8", wantCIDR: "10.0.0.0/8",
		},
		{
			name:     "bare domain",
			input:    "example.com",
			wantHost: "example.com", wantDomain: true,
		},
		{
			name:     "wildcard domain",
			input:    "*.example.com",
			wantHost: "*.example.com", wantDomain: true,
		},
		{
			name:     "all traffic CIDR",
			input:    "0.0.0.0/0",
			wantHost: "0.0.0.0/0", wantCIDR: "0.0.0.0/0",
		},

		// Host with single port
		{
			name:     "IP with port",
			input:    "8.8.8.8:53",
			wantHost: "8.8.8.8", wantPort: [2]uint16{53, 53}, wantCIDR: "8.8.8.8/32",
		},
		{
			name:     "CIDR with port",
			input:    "10.0.0.0/8:80",
			wantHost: "10.0.0.0/8", wantPort: [2]uint16{80, 80}, wantCIDR: "10.0.0.0/8",
		},
		{
			name:     "domain with port",
			input:    "example.com:443",
			wantHost: "example.com", wantPort: [2]uint16{443, 443}, wantDomain: true,
		},
		{
			name:     "wildcard domain with port",
			input:    "*.example.com:80",
			wantHost: "*.example.com", wantPort: [2]uint16{80, 80}, wantDomain: true,
		},

		// Host with port range
		{
			name:     "IP with port range",
			input:    "8.8.8.8:1-1024",
			wantHost: "8.8.8.8", wantPort: [2]uint16{1, 1024}, wantCIDR: "8.8.8.8/32",
		},
		{
			name:     "CIDR with port range",
			input:    "10.0.0.0/8:80-443",
			wantHost: "10.0.0.0/8", wantPort: [2]uint16{80, 443}, wantCIDR: "10.0.0.0/8",
		},
		{
			name:     "domain with port range",
			input:    "example.com:8080-9090",
			wantHost: "example.com", wantPort: [2]uint16{8080, 9090}, wantDomain: true,
		},
		{
			name:     "single port range",
			input:    "8.8.8.8:80-80",
			wantHost: "8.8.8.8", wantPort: [2]uint16{80, 80}, wantCIDR: "8.8.8.8/32",
		},

		// Explicit all ports (trailing colon)
		{
			name:     "IP with trailing colon",
			input:    "8.8.8.8:",
			wantHost: "8.8.8.8", wantCIDR: "8.8.8.8/32",
		},
		{
			name:     "CIDR with trailing colon",
			input:    "10.0.0.0/8:",
			wantHost: "10.0.0.0/8", wantCIDR: "10.0.0.0/8",
		},

		// Empty host means all IPs (0.0.0.0/0)
		{
			name:     "port only",
			input:    ":443",
			wantHost: "0.0.0.0/0", wantPort: [2]uint16{443, 443}, wantCIDR: "0.0.0.0/0",
		},
		{
			name:     "port range only",
			input:    ":80-443",
			wantHost: "0.0.0.0/0", wantPort: [2]uint16{80, 443}, wantCIDR: "0.0.0.0/0",
		},
		{
			name:     "bare colon means all IPs all ports",
			input:    ":",
			wantHost: "0.0.0.0/0", wantCIDR: "0.0.0.0/0",
		},

		// Max port
		{
			name:     "max port",
			input:    "8.8.8.8:65535",
			wantHost: "8.8.8.8", wantPort: [2]uint16{65535, 65535}, wantCIDR: "8.8.8.8/32",
		},
		{
			name:     "full port range",
			input:    "8.8.8.8:1-65535",
			wantHost: "8.8.8.8", wantPort: [2]uint16{1, 65535}, wantCIDR: "8.8.8.8/32",
		},

		// IPv6 addresses
		{
			name:     "bare IPv6 CIDR",
			input:    "::/0",
			wantHost: "::/0", wantCIDR: "::/0",
		},
		{
			name:     "IPv6 loopback",
			input:    "::1",
			wantHost: "::1", wantCIDR: "::1/128",
		},
		{
			name:     "IPv6 half range",
			input:    "8000::/1",
			wantHost: "8000::/1", wantCIDR: "8000::/1",
		},
		{
			name:     "IPv6 with port in brackets",
			input:    "[::1]:80",
			wantHost: "::1", wantPort: [2]uint16{80, 80}, wantCIDR: "::1/128",
		},
		{
			name:     "IPv6 CIDR with port in brackets",
			input:    "[::/0]:443",
			wantHost: "::/0", wantPort: [2]uint16{443, 443}, wantCIDR: "::/0",
		},
		{
			name:     "IPv6 with port range in brackets",
			input:    "[::1]:80-443",
			wantHost: "::1", wantPort: [2]uint16{80, 443}, wantCIDR: "::1/128",
		},
		{
			name:     "IPv6 with trailing colon in brackets",
			input:    "[::1]:",
			wantHost: "::1", wantCIDR: "::1/128",
		},
		{
			name:     "bracketed IPv6 no port",
			input:    "[::1]",
			wantHost: "::1", wantCIDR: "::1/128",
		},

		// Errors
		{name: "empty string", input: "", wantErr: true, errSubstr: "empty"},
		{name: "port zero", input: "8.8.8.8:0", wantErr: true, errSubstr: "port must be between 1 and 65535"},
		{name: "port too high", input: "8.8.8.8:65536", wantErr: true, errSubstr: "invalid port"},
		{name: "port range reversed", input: "8.8.8.8:1024-80", wantErr: true, errSubstr: "greater than end"},
		{name: "non-numeric port", input: "8.8.8.8:abc", wantErr: true, errSubstr: "invalid port"},
		{name: "port range with non-numeric start", input: "8.8.8.8:abc-100", wantErr: true, errSubstr: "invalid port"},
		{name: "port range with non-numeric end", input: "8.8.8.8:80-abc", wantErr: true, errSubstr: "invalid port"},
		{name: "port range start zero", input: "8.8.8.8:0-80", wantErr: true, errSubstr: "port must be between 1 and 65535"},
		{name: "missing closing bracket", input: "[::1", wantErr: true, errSubstr: "missing closing bracket"},
		{name: "junk after bracket", input: "[::1]x", wantErr: true, errSubstr: "expected ':'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseRule(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSubstr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantHost, got.Host)
			require.Equal(t, tt.wantPort[0], got.PortStart)
			require.Equal(t, tt.wantPort[1], got.PortEnd)
			require.Equal(t, tt.wantDomain, got.IsDomain)

			if tt.wantDomain {
				require.Nil(t, got.IPNet)
			} else {
				require.NotNil(t, got.IPNet)
				require.Equal(t, tt.wantCIDR, got.IPNet.String())
			}
		})
	}
}

func TestParseRules(t *testing.T) {
	t.Parallel()

	t.Run("multiple valid entries", func(t *testing.T) {
		t.Parallel()

		rules, err := ParseRules([]string{
			"8.8.8.8:53",
			"10.0.0.0/8",
			"example.com:443",
		})
		require.NoError(t, err)
		require.Len(t, rules, 3)

		require.Equal(t, "8.8.8.8", rules[0].Host)
		require.Equal(t, uint16(53), rules[0].PortStart)
		require.False(t, rules[0].IsDomain)

		require.Equal(t, "10.0.0.0/8", rules[1].Host)
		require.True(t, rules[1].AllPorts())
		require.False(t, rules[1].IsDomain)

		require.Equal(t, "example.com", rules[2].Host)
		require.Equal(t, uint16(443), rules[2].PortStart)
		require.True(t, rules[2].IsDomain)
	})

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()

		rules, err := ParseRules(nil)
		require.NoError(t, err)
		require.Empty(t, rules)
	})

	t.Run("invalid entry returns error", func(t *testing.T) {
		t.Parallel()

		_, err := ParseRules([]string{"8.8.8.8:80", "8.8.8.8:abc"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "8.8.8.8:abc")
	})
}

func TestNewEgressACL_RejectsDomainInDeny(t *testing.T) {
	t.Parallel()

	_, err := NewEgressACL(nil, []string{"example.com"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "domain entries are not supported")

	_, err = NewEgressACL(nil, []string{"0.0.0.0/0", "*.example.com:443"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "domain entries are not supported")

	// IPs and CIDRs in deny are fine.
	acl, err := NewEgressACL([]string{"example.com:443"}, []string{"10.0.0.0/8", "192.168.1.1:80"})
	require.NoError(t, err)
	require.Len(t, acl.Denied, 2)
}

func TestNewEgressACL_RejectsIPv6(t *testing.T) {
	t.Parallel()

	_, err := NewEgressACL([]string{"::1"}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "IPv6")

	_, err = NewEgressACL(nil, []string{"::1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "IPv6")

	_, err = NewEgressACL([]string{"[::1]:80"}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "IPv6")
}

func TestNewIngressACL(t *testing.T) {
	t.Parallel()

	t.Run("nil for empty inputs", func(t *testing.T) {
		t.Parallel()

		acl, err := NewIngressACL(nil, nil)
		require.NoError(t, err)
		require.Nil(t, acl)
	})

	t.Run("rejects domains", func(t *testing.T) {
		t.Parallel()

		_, err := NewIngressACL([]string{"example.com"}, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "domain")

		_, err = NewIngressACL(nil, []string{"example.com"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "domain")
	})

	t.Run("accepts IPv4", func(t *testing.T) {
		t.Parallel()

		acl, err := NewIngressACL([]string{"10.0.0.0/8"}, []string{"0.0.0.0/0"})
		require.NoError(t, err)
		require.Len(t, acl.Allowed, 1)
		require.Len(t, acl.Denied, 1)
	})

	t.Run("accepts IPv6", func(t *testing.T) {
		t.Parallel()

		acl, err := NewIngressACL([]string{"::1", "[2001:db8::1]:80"}, []string{"::/0"})
		require.NoError(t, err)
		require.Len(t, acl.Allowed, 2)
		require.Len(t, acl.Denied, 1)
	})
}

func TestACL_IsAllowed(t *testing.T) {
	t.Parallel()

	t.Run("nil ACL allows all", func(t *testing.T) {
		t.Parallel()

		var acl *ACL
		require.True(t, acl.IsAllowed(net.ParseIP("1.2.3.4"), 80))
	})

	t.Run("allow wins over deny", func(t *testing.T) {
		t.Parallel()

		acl, err := NewIngressACL([]string{"10.0.0.0/8"}, []string{"0.0.0.0/0"})
		require.NoError(t, err)

		require.True(t, acl.IsAllowed(net.ParseIP("10.1.2.3"), 80))
		require.False(t, acl.IsAllowed(net.ParseIP("192.168.1.1"), 80))
	})

	t.Run("port-specific rules", func(t *testing.T) {
		t.Parallel()

		acl, err := NewIngressACL([]string{"0.0.0.0/0:443"}, []string{"0.0.0.0/0"})
		require.NoError(t, err)

		require.True(t, acl.IsAllowed(net.ParseIP("1.2.3.4"), 443))
		require.False(t, acl.IsAllowed(net.ParseIP("1.2.3.4"), 80))
	})

	t.Run("IPv6 matching", func(t *testing.T) {
		t.Parallel()

		acl, err := NewIngressACL([]string{"2001:db8::/32"}, []string{"::/0"})
		require.NoError(t, err)

		require.True(t, acl.IsAllowed(net.ParseIP("2001:db8::1"), 80))
		require.False(t, acl.IsAllowed(net.ParseIP("2001:db9::1"), 80))
	})
}

func TestRule_AllPorts(t *testing.T) {
	t.Parallel()

	require.True(t, Rule{Host: "8.8.8.8", PortStart: 0, PortEnd: 0}.AllPorts())
	require.False(t, Rule{Host: "8.8.8.8", PortStart: 80, PortEnd: 80}.AllPorts())
	require.False(t, Rule{Host: "8.8.8.8", PortStart: 1, PortEnd: 1024}.AllPorts())
}

func TestRule_ContainsIP(t *testing.T) {
	t.Parallel()

	rule, err := ParseRule("10.0.0.0/8")
	require.NoError(t, err)

	require.True(t, rule.ContainsIP(net.ParseIP("10.1.2.3")))
	require.False(t, rule.ContainsIP(net.ParseIP("192.168.1.1")))

	// Domain rules never contain IPs.
	domainRule, err := ParseRule("example.com")
	require.NoError(t, err)
	require.False(t, domainRule.ContainsIP(net.ParseIP("1.2.3.4")))
}
