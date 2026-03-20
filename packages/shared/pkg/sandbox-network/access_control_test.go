package sandbox_network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitHostPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantHost string
		wantPort string
	}{
		{"bare IP", "8.8.8.8", "8.8.8.8", ""},
		{"bare CIDR", "10.0.0.0/8", "10.0.0.0/8", ""},
		{"bare domain", "example.com", "example.com", ""},
		{"IP with port", "8.8.8.8:80", "8.8.8.8", "80"},
		{"CIDR with port", "10.0.0.0/8:80", "10.0.0.0/8", "80"},
		{"domain with port", "example.com:443", "example.com", "443"},
		{"IP with port range", "8.8.8.8:80-443", "8.8.8.8", "80-443"},
		{"trailing colon", "8.8.8.8:", "8.8.8.8", ""},
		{"port only", ":443", "0.0.0.0/0", "443"},
		{"bare colon", ":", "0.0.0.0/0", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, port, err := SplitHostPort(tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.wantHost, host)
			require.Equal(t, tt.wantPort, port)
		})
	}
}

func TestParsePortRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantLow   uint16
		wantHigh  uint16
		wantErr   bool
		errSubstr string
	}{
		{"single port", "80", 80, 80, false, ""},
		{"port range", "80-443", 80, 443, false, ""},
		{"same port range", "80-80", 80, 80, false, ""},
		{"max port", "65535", 65535, 65535, false, ""},
		{"full range", "1-65535", 1, 65535, false, ""},
		{"port zero", "0", 0, 0, true, "port 0"},
		{"port too high", "65536", 0, 0, true, "invalid port"},
		{"reversed range", "443-80", 0, 0, true, "start > end"},
		{"non-numeric", "abc", 0, 0, true, "invalid port"},
		{"range start zero", "0-80", 0, 0, true, "port 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lo, hi, err := ParsePortRange(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSubstr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantLow, lo)
			require.Equal(t, tt.wantHigh, hi)
		})
	}
}

func TestIngressIsAllowed(t *testing.T) {
	t.Parallel()

	mustParseCIDR := func(s string) *net.IPNet {
		_, ipNet, err := net.ParseCIDR(s)
		require.NoError(t, err)

		return ipNet
	}

	t.Run("no rules allows all", func(t *testing.T) {
		t.Parallel()

		ingress := &Ingress{}
		require.True(t, ingress.IsAllowed(net.ParseIP("1.2.3.4"), 80))
	})

	t.Run("allow takes precedence over deny", func(t *testing.T) {
		t.Parallel()

		ingress := &Ingress{
			Allowed: Rules{{IPNet: mustParseCIDR("10.0.0.0/8")}},
			Denied:  Rules{{IPNet: mustParseCIDR("0.0.0.0/0")}},
		}

		require.True(t, ingress.IsAllowed(net.ParseIP("10.1.2.3"), 80))
		require.False(t, ingress.IsAllowed(net.ParseIP("192.168.1.1"), 80))
	})

	t.Run("port-scoped allow with deny-all", func(t *testing.T) {
		t.Parallel()

		ingress := &Ingress{
			Allowed: Rules{{IPNet: mustParseCIDR("0.0.0.0/0"), PortStart: 443, PortEnd: 443}},
			Denied:  Rules{{IPNet: mustParseCIDR("0.0.0.0/0")}},
		}

		require.True(t, ingress.IsAllowed(net.ParseIP("1.2.3.4"), 443))
		require.False(t, ingress.IsAllowed(net.ParseIP("1.2.3.4"), 80))
	})
}

func TestEgressMatchDomain(t *testing.T) {
	t.Parallel()

	egress := Egress{
		AllowedHTTPHostDomains: []string{"example.com", "*.test.com"},
	}

	require.True(t, egress.MatchDomain("example.com"))
	require.True(t, egress.MatchDomain("foo.test.com"))
	require.False(t, egress.MatchDomain("other.com"))
}

func TestRulePortInRange(t *testing.T) {
	t.Parallel()

	// No port constraint matches any port.
	require.True(t, Rule{}.PortInRange(80))
	require.True(t, Rule{}.PortInRange(443))

	// Specific port.
	require.True(t, Rule{PortStart: 80, PortEnd: 80}.PortInRange(80))
	require.False(t, Rule{PortStart: 80, PortEnd: 80}.PortInRange(443))

	// Port range.
	require.True(t, Rule{PortStart: 80, PortEnd: 443}.PortInRange(200))
	require.False(t, Rule{PortStart: 80, PortEnd: 443}.PortInRange(8080))
}
