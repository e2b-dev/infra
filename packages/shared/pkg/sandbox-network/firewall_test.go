package sandbox_network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSpecifiedIPOrCIDR(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid_ip", "1.2.3.4", true},
		{"valid_cidr", "10.0.0.0/8", true},
		{"valid_host_cidr", "192.168.1.1/32", true},
		{"all_traffic_cidr", "0.0.0.0/0", true},
		{"unspecified_ip", "0.0.0.0", false},
		{"unspecified_cidr_32", "0.0.0.0/32", false},
		{"unspecified_cidr_24", "0.0.0.0/24", false},
		{"unspecified_ipv6", "::", false},
		{"unspecified_ipv6_128", "::/128", false},
		{"invalid_string", "not-an-ip", false},
		{"empty_string", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := IsSpecifiedIPOrCIDR(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestAddressStringToCIDR(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		input    string
		expected string
		desc     string
	}{
		{
			name:     "already_has_cidr",
			input:    "192.168.1.1/24",
			expected: "192.168.1.1/24",
			desc:     "address with CIDR should remain unchanged",
		},
		{
			name:     "ip_without_cidr",
			input:    "8.8.8.8",
			expected: "8.8.8.8/32",
			desc:     "IP without CIDR should append /32",
		},
		{
			name:     "invalid_format_no_validation",
			input:    "not.an.ip.address",
			expected: "not.an.ip.address/32",
			desc:     "invalid format should still append /32 (function doesn't validate)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := AddressStringToCIDR(tc.input)
			require.Equal(t, tc.expected, result, tc.desc)
		})
	}
}

func TestParseCIDRs(t *testing.T) {
	t.Parallel()

	t.Run("valid entries", func(t *testing.T) {
		t.Parallel()
		nets, err := ParseCIDRs([]string{"10.0.11.254/32", "192.168.0.0/16", "8.8.8.8", "fc00::/7", "::1"})
		require.NoError(t, err)
		require.Len(t, nets, 5)

		require.True(t, IPInNets(net.ParseIP("10.0.11.254"), nets))
		require.False(t, IPInNets(net.ParseIP("10.0.11.253"), nets))
		require.True(t, IPInNets(net.ParseIP("192.168.42.1"), nets))
		require.True(t, IPInNets(net.ParseIP("8.8.8.8"), nets), "bare IPv4 should be treated as /32")
		require.False(t, IPInNets(net.ParseIP("8.8.8.9"), nets))
		require.True(t, IPInNets(net.ParseIP("fc00::1"), nets))
		require.True(t, IPInNets(net.ParseIP("::1"), nets), "bare IPv6 should be treated as /128")
		require.False(t, IPInNets(net.ParseIP("::2"), nets))
	})

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()
		nets, err := ParseCIDRs(nil)
		require.NoError(t, err)
		require.Empty(t, nets)
		require.False(t, IPInNets(net.ParseIP("10.0.11.254"), nets))
	})

	t.Run("invalid entry", func(t *testing.T) {
		t.Parallel()
		_, err := ParseCIDRs([]string{"10.0.11.254/32", "not-a-cidr"})
		require.Error(t, err)
	})
}
