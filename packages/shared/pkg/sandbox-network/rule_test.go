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
		{"bare IPv6 CIDR", "::/0", "::/0", ""},
		{"IPv6 loopback", "::1", "::1", ""},
		{"bracketed IPv6 with port", "[::1]:80", "::1", "80"},
		{"bracketed IPv6 no port", "[::1]", "::1", ""},
		{"bracketed IPv6 CIDR with port", "[::/0]:443", "::/0", "443"},
		{"bracketed IPv6 with range", "[::1]:80-443", "::1", "80-443"},
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

func TestACL_IsAllowed(t *testing.T) {
	t.Parallel()

	mustParseCIDR := func(s string) *net.IPNet {
		_, ipNet, err := net.ParseCIDR(s)
		require.NoError(t, err)

		return ipNet
	}

	t.Run("nil ACL allows all", func(t *testing.T) {
		t.Parallel()

		var acl *ACL
		require.True(t, acl.IsAllowed(net.ParseIP("1.2.3.4"), 80))
	})

	t.Run("allow wins over deny", func(t *testing.T) {
		t.Parallel()

		acl := &ACL{
			Allowed: []Rule{{IPNet: mustParseCIDR("10.0.0.0/8")}},
			Denied:  []Rule{{IPNet: mustParseCIDR("0.0.0.0/0")}},
		}

		require.True(t, acl.IsAllowed(net.ParseIP("10.1.2.3"), 80))
		require.False(t, acl.IsAllowed(net.ParseIP("192.168.1.1"), 80))
	})

	t.Run("port-specific rules", func(t *testing.T) {
		t.Parallel()

		acl := &ACL{
			Allowed: []Rule{{IPNet: mustParseCIDR("0.0.0.0/0"), PortStart: 443, PortEnd: 443}},
			Denied:  []Rule{{IPNet: mustParseCIDR("0.0.0.0/0")}},
		}

		require.True(t, acl.IsAllowed(net.ParseIP("1.2.3.4"), 443))
		require.False(t, acl.IsAllowed(net.ParseIP("1.2.3.4"), 80))
	})

	t.Run("IPv6 matching", func(t *testing.T) {
		t.Parallel()

		acl := &ACL{
			Allowed: []Rule{{IPNet: mustParseCIDR("2001:db8::/32")}},
			Denied:  []Rule{{IPNet: mustParseCIDR("::/0")}},
		}

		require.True(t, acl.IsAllowed(net.ParseIP("2001:db8::1"), 80))
		require.False(t, acl.IsAllowed(net.ParseIP("2001:db9::1"), 80))
	})
}

func TestRule_AllPorts(t *testing.T) {
	t.Parallel()

	require.True(t, Rule{PortStart: 0, PortEnd: 0}.AllPorts())
	require.False(t, Rule{PortStart: 80, PortEnd: 80}.AllPorts())
}

func TestRule_HasPort(t *testing.T) {
	t.Parallel()

	require.False(t, Rule{PortStart: 0, PortEnd: 0}.HasPort())
	require.True(t, Rule{PortStart: 80, PortEnd: 80}.HasPort())
}
