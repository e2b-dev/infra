package sandbox_network

import (
	"strings"
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

func TestMatchDomainPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hostname string
		pattern  string
		want     bool
	}{
		{name: "exact", hostname: "example.com", pattern: "example.com", want: true},
		{name: "exact case folded", hostname: "Example.COM", pattern: "example.com", want: true},
		{name: "exact uppercase pattern", hostname: "example.com", pattern: "EXAMPLE.COM", want: true},
		{name: "different exact domain", hostname: "example.com", pattern: "other.com", want: false},
		{name: "bare wildcard", hostname: "anything.example.com", pattern: "*", want: true},
		{name: "bare wildcard matches simple hostname", hostname: "localhost", pattern: "*", want: true},
		{name: "bare wildcard excludes empty hostname", hostname: "", pattern: "*", want: false},
		{name: "nested wildcard", hostname: "deep.nested.example.com", pattern: "*.example.com", want: true},
		{name: "wildcard case folded", hostname: "API.EXAMPLE.COM", pattern: "*.example.com", want: true},
		{name: "wildcard excludes apex", hostname: "example.com", pattern: "*.example.com", want: false},
		{name: "wildcard excludes unrelated host", hostname: "api.other.com", pattern: "*.example.com", want: false},
		{name: "wildcard excludes partial suffix", hostname: "notexample.com", pattern: "*.example.com", want: false},
		{name: "broad wildcard", hostname: "example.com", pattern: "*.com", want: true},
		{name: "service wildcard", hostname: "bucket.s3.amazonaws.com", pattern: "*.s3.amazonaws.com", want: true},
		{name: "empty pattern", hostname: "example.com", pattern: "", want: false},
		{name: "empty values", hostname: "", pattern: "", want: false},
		{name: "dot-prefixed hostname", hostname: ".example.com", pattern: "*.example.com", want: false},
		{name: "empty hostname label", hostname: "api..example.com", pattern: "*.example.com", want: false},
		{name: "empty pattern label", hostname: "api.example.com", pattern: "*.example..com", want: false},
		{name: "relative pattern excludes absolute hostname", hostname: "api.example.com.", pattern: "*.example.com", want: false},
		{name: "absolute pattern excludes relative hostname", hostname: "api.example.com", pattern: "*.example.com.", want: false},
		{name: "absolute wildcard", hostname: "api.example.com.", pattern: "*.example.com.", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, MatchDomainPattern(tt.hostname, tt.pattern))
		})
	}
}

func TestIsValidWildcardDomainPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		want    bool
	}{
		{pattern: "*.github.com", want: true},
		{pattern: "*.GITHUB.COM", want: true},
		{pattern: "*.com", want: true},
		{pattern: "*.s3.amazonaws.com", want: true},
		{pattern: "*.internal", want: true},
		{pattern: "*", want: false},
		{pattern: "*.", want: false},
		{pattern: "*.*.com", want: false},
		{pattern: "api*.com", want: false},
		{pattern: "github.*", want: false},
		{pattern: "*.github.com.", want: false},
		{pattern: "*.K.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, IsValidWildcardDomainPattern(tt.pattern))
		})
	}

	t.Run("length boundary", func(t *testing.T) {
		t.Parallel()

		maxLength := "*." + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 59)
		require.Len(t, maxLength, 253)
		require.True(t, IsValidWildcardDomainPattern(maxLength))
		require.False(t, IsValidWildcardDomainPattern(maxLength+"d"))
	})
}
