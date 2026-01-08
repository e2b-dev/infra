package sandbox_network

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
