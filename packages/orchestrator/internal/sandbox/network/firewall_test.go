package network

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCanAllowAddress_PrivateRangesBlocked tests that private IP ranges cannot be allowed
// Private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16) are always blocked
// by the orchestrator for security reasons, even if specified in allowOut
func TestCanAllowAddress_PrivateRangesBlocked(t *testing.T) {
	testCases := []struct {
		name      string
		address   string
		shouldErr bool
		desc      string
	}{
		{
			name:      "private_range_10.0.0.0/8",
			address:   "10.0.0.0/8",
			shouldErr: true,
			desc:      "10.0.0.0/8 range should be blocked",
		},
		{
			name:      "private_range_192.168.0.0/16",
			address:   "192.168.0.0/16",
			shouldErr: true,
			desc:      "192.168.0.0/16 range should be blocked",
		},
		{
			name:      "private_range_172.16.0.0/12",
			address:   "172.16.0.0/12",
			shouldErr: true,
			desc:      "172.16.0.0/12 range should be blocked",
		},
		{
			name:      "link_local_169.254.0.0/16",
			address:   "169.254.0.0/16",
			shouldErr: true,
			desc:      "169.254.0.0/16 range (link-local) should be blocked",
		},
		{
			name:      "specific_ip_in_10.0.0.0/8",
			address:   "10.1.2.3/32",
			shouldErr: true,
			desc:      "specific IP in 10.0.0.0/8 range should be blocked",
		},
		{
			name:      "specific_ip_in_192.168.0.0/16",
			address:   "192.168.1.1/32",
			shouldErr: true,
			desc:      "specific IP in 192.168.0.0/16 range should be blocked",
		},
		{
			name:      "specific_ip_in_172.16.0.0/12",
			address:   "172.16.0.1/32",
			shouldErr: true,
			desc:      "specific IP in 172.16.0.0/12 range should be blocked",
		},
		{
			name:      "metadata_service_ip_169.254.169.254",
			address:   "169.254.169.254/32",
			shouldErr: true,
			desc:      "metadata service IP should be blocked",
		},
		{
			name:      "public_ip_8.8.8.8",
			address:   "8.8.8.8/32",
			shouldErr: false,
			desc:      "public IP should be allowed",
		},
		{
			name:      "public_ip_1.1.1.1",
			address:   "1.1.1.1/32",
			shouldErr: false,
			desc:      "public IP should be allowed",
		},
		{
			name:      "public_range_1.1.1.0/24",
			address:   "1.1.1.0/24",
			shouldErr: false,
			desc:      "public CIDR range should be allowed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := canAllowCIDR(tc.address)
			if tc.shouldErr {
				require.Error(t, err, tc.desc)
				require.Contains(t, err.Error(), "blocked by the provider", "Error message should indicate provider blocking")
			} else {
				require.NoError(t, err, tc.desc)
			}
		})
	}
}

// TestCanAllowAddress_InvalidAddresses tests that invalid addresses return errors
func TestCanAllowAddress_InvalidAddresses(t *testing.T) {
	testCases := []struct {
		name    string
		address string
		desc    string
	}{
		{
			name:    "invalid_format",
			address: "not-an-ip",
			desc:    "invalid IP format should return error",
		},
		{
			name:    "invalid_cidr",
			address: "256.256.256.256/24",
			desc:    "invalid IP in CIDR should return error",
		},
		{
			name:    "empty_string",
			address: "",
			desc:    "empty string should return error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := canAllowCIDR(tc.address)
			require.Error(t, err, tc.desc)
		})
	}
}
