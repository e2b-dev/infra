package sandbox_network

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      Rule
		wantErr   bool
		errSubstr string
	}{
		// Backwards-compatible: bare host, all ports
		{
			name:  "bare IP",
			input: "8.8.8.8",
			want:  Rule{Host: "8.8.8.8", PortStart: 0, PortEnd: 0, IsDomain: false},
		},
		{
			name:  "bare CIDR",
			input: "10.0.0.0/8",
			want:  Rule{Host: "10.0.0.0/8", PortStart: 0, PortEnd: 0, IsDomain: false},
		},
		{
			name:  "bare domain",
			input: "example.com",
			want:  Rule{Host: "example.com", PortStart: 0, PortEnd: 0, IsDomain: true},
		},
		{
			name:  "wildcard domain",
			input: "*.example.com",
			want:  Rule{Host: "*.example.com", PortStart: 0, PortEnd: 0, IsDomain: true},
		},
		{
			name:  "all traffic CIDR",
			input: "0.0.0.0/0",
			want:  Rule{Host: "0.0.0.0/0", PortStart: 0, PortEnd: 0, IsDomain: false},
		},

		// Host with single port
		{
			name:  "IP with port",
			input: "8.8.8.8:53",
			want:  Rule{Host: "8.8.8.8", PortStart: 53, PortEnd: 53, IsDomain: false},
		},
		{
			name:  "CIDR with port",
			input: "10.0.0.0/8:80",
			want:  Rule{Host: "10.0.0.0/8", PortStart: 80, PortEnd: 80, IsDomain: false},
		},
		{
			name:  "domain with port",
			input: "example.com:443",
			want:  Rule{Host: "example.com", PortStart: 443, PortEnd: 443, IsDomain: true},
		},
		{
			name:  "wildcard domain with port",
			input: "*.example.com:80",
			want:  Rule{Host: "*.example.com", PortStart: 80, PortEnd: 80, IsDomain: true},
		},

		// Host with port range
		{
			name:  "IP with port range",
			input: "8.8.8.8:1-1024",
			want:  Rule{Host: "8.8.8.8", PortStart: 1, PortEnd: 1024, IsDomain: false},
		},
		{
			name:  "CIDR with port range",
			input: "10.0.0.0/8:80-443",
			want:  Rule{Host: "10.0.0.0/8", PortStart: 80, PortEnd: 443, IsDomain: false},
		},
		{
			name:  "domain with port range",
			input: "example.com:8080-9090",
			want:  Rule{Host: "example.com", PortStart: 8080, PortEnd: 9090, IsDomain: true},
		},
		{
			name:  "single port range",
			input: "8.8.8.8:80-80",
			want:  Rule{Host: "8.8.8.8", PortStart: 80, PortEnd: 80, IsDomain: false},
		},

		// Explicit all ports (trailing colon)
		{
			name:  "IP with trailing colon",
			input: "8.8.8.8:",
			want:  Rule{Host: "8.8.8.8", PortStart: 0, PortEnd: 0, IsDomain: false},
		},
		{
			name:  "CIDR with trailing colon",
			input: "10.0.0.0/8:",
			want:  Rule{Host: "10.0.0.0/8", PortStart: 0, PortEnd: 0, IsDomain: false},
		},

		// Max port
		{
			name:  "max port",
			input: "8.8.8.8:65535",
			want:  Rule{Host: "8.8.8.8", PortStart: 65535, PortEnd: 65535, IsDomain: false},
		},
		{
			name:  "full port range",
			input: "8.8.8.8:1-65535",
			want:  Rule{Host: "8.8.8.8", PortStart: 1, PortEnd: 65535, IsDomain: false},
		},

		// IPv6 addresses
		{
			name:  "bare IPv6 CIDR",
			input: "::/0",
			want:  Rule{Host: "::/0", PortStart: 0, PortEnd: 0, IsDomain: false},
		},
		{
			name:  "IPv6 loopback",
			input: "::1",
			want:  Rule{Host: "::1", PortStart: 0, PortEnd: 0, IsDomain: false},
		},
		{
			name:  "IPv6 half range",
			input: "8000::/1",
			want:  Rule{Host: "8000::/1", PortStart: 0, PortEnd: 0, IsDomain: false},
		},
		{
			name:  "IPv6 with port in brackets",
			input: "[::1]:80",
			want:  Rule{Host: "::1", PortStart: 80, PortEnd: 80, IsDomain: false},
		},
		{
			name:  "IPv6 CIDR with port in brackets",
			input: "[::/0]:443",
			want:  Rule{Host: "::/0", PortStart: 443, PortEnd: 443, IsDomain: false},
		},
		{
			name:  "IPv6 with port range in brackets",
			input: "[::1]:80-443",
			want:  Rule{Host: "::1", PortStart: 80, PortEnd: 443, IsDomain: false},
		},
		{
			name:  "IPv6 with trailing colon in brackets",
			input: "[::1]:",
			want:  Rule{Host: "::1", PortStart: 0, PortEnd: 0, IsDomain: false},
		},
		{
			name:  "bracketed IPv6 no port",
			input: "[::1]",
			want:  Rule{Host: "::1", PortStart: 0, PortEnd: 0, IsDomain: false},
		},

		// Errors
		{
			name:      "empty string",
			input:     "",
			wantErr:   true,
			errSubstr: "empty",
		},
		{
			name:      "port zero",
			input:     "8.8.8.8:0",
			wantErr:   true,
			errSubstr: "port must be between 1 and 65535",
		},
		{
			name:      "port too high",
			input:     "8.8.8.8:65536",
			wantErr:   true,
			errSubstr: "invalid port",
		},
		{
			name:      "port range reversed",
			input:     "8.8.8.8:1024-80",
			wantErr:   true,
			errSubstr: "greater than end",
		},
		{
			name:      "non-numeric port",
			input:     "8.8.8.8:abc",
			wantErr:   true,
			errSubstr: "invalid port",
		},
		{
			name:      "port range with non-numeric start",
			input:     "8.8.8.8:abc-100",
			wantErr:   true,
			errSubstr: "invalid port",
		},
		{
			name:      "port range with non-numeric end",
			input:     "8.8.8.8:80-abc",
			wantErr:   true,
			errSubstr: "invalid port",
		},
		{
			name:      "port range start zero",
			input:     "8.8.8.8:0-80",
			wantErr:   true,
			errSubstr: "port must be between 1 and 65535",
		},
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
			require.Equal(t, tt.want, got)
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
		require.Equal(t, Rule{Host: "8.8.8.8", PortStart: 53, PortEnd: 53, IsDomain: false}, rules[0])
		require.Equal(t, Rule{Host: "10.0.0.0/8", PortStart: 0, PortEnd: 0, IsDomain: false}, rules[1])
		require.Equal(t, Rule{Host: "example.com", PortStart: 443, PortEnd: 443, IsDomain: true}, rules[2])
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
