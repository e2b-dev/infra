package sandbox_network

import (
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
