package sandbox_network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
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

		// Errors
		{name: "empty string", input: "", wantErr: true, errSubstr: "empty"},
		{name: "port zero", input: "8.8.8.8:0", wantErr: true, errSubstr: "port must be between 1 and 65535"},
		{name: "port too high", input: "8.8.8.8:65536", wantErr: true, errSubstr: "invalid port"},
		{name: "port range reversed", input: "8.8.8.8:1024-80", wantErr: true, errSubstr: "greater than end"},
		{name: "non-numeric port", input: "8.8.8.8:abc", wantErr: true, errSubstr: "invalid port"},
		{name: "port range with non-numeric start", input: "8.8.8.8:abc-100", wantErr: true, errSubstr: "invalid port"},
		{name: "port range with non-numeric end", input: "8.8.8.8:80-abc", wantErr: true, errSubstr: "invalid port"},
		{name: "port range start zero", input: "8.8.8.8:0-80", wantErr: true, errSubstr: "port must be between 1 and 65535"},

		// IPv6 rejection
		{name: "bare IPv6", input: "::1", wantErr: true, errSubstr: "IPv6"},
		{name: "bare IPv6 full", input: "2001:db8::1", wantErr: true, errSubstr: "IPv6"},
		{name: "bracketed IPv6", input: "[::1]:80", wantErr: true, errSubstr: "IPv6"},
		{name: "bracketed IPv6 no port", input: "[2001:db8::1]", wantErr: true, errSubstr: "IPv6"},
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
			assert.Equal(t, tt.wantHost, got.Host)
			assert.Equal(t, tt.wantPort[0], got.PortStart)
			assert.Equal(t, tt.wantPort[1], got.PortEnd)
			assert.Equal(t, tt.wantDomain, got.IsDomain)

			if tt.wantDomain {
				assert.Nil(t, got.IPNet)
			} else {
				require.NotNil(t, got.IPNet)
				assert.Equal(t, tt.wantCIDR, got.IPNet.String())
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

		assert.Equal(t, "8.8.8.8", rules[0].Host)
		assert.Equal(t, uint16(53), rules[0].PortStart)
		assert.False(t, rules[0].IsDomain)

		assert.Equal(t, "10.0.0.0/8", rules[1].Host)
		assert.True(t, rules[1].AllPorts())
		assert.False(t, rules[1].IsDomain)

		assert.Equal(t, "example.com", rules[2].Host)
		assert.Equal(t, uint16(443), rules[2].PortStart)
		assert.True(t, rules[2].IsDomain)
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

	assert.True(t, rule.ContainsIP(net.ParseIP("10.1.2.3")))
	assert.False(t, rule.ContainsIP(net.ParseIP("192.168.1.1")))

	// Domain rules never contain IPs.
	domainRule, err := ParseRule("example.com")
	require.NoError(t, err)
	assert.False(t, domainRule.ContainsIP(net.ParseIP("1.2.3.4")))
}
