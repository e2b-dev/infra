package tcpfirewall

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

func TestMatchDomain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		hostname string
		pattern  string
		want     bool
	}{
		// Exact matches
		{
			name:     "exact match",
			hostname: "example.com",
			pattern:  "example.com",
			want:     true,
		},
		{
			name:     "exact match case insensitive",
			hostname: "Example.COM",
			pattern:  "example.com",
			want:     true,
		},
		{
			name:     "exact match pattern uppercase",
			hostname: "example.com",
			pattern:  "EXAMPLE.COM",
			want:     true,
		},
		{
			name:     "no match different domain",
			hostname: "example.com",
			pattern:  "other.com",
			want:     false,
		},

		// Wildcard *
		{
			name:     "wildcard matches any hostname",
			hostname: "anything.example.com",
			pattern:  "*",
			want:     true,
		},
		{
			name:     "wildcard matches simple hostname",
			hostname: "localhost",
			pattern:  "*",
			want:     true,
		},

		// Suffix wildcards *.domain
		{
			name:     "suffix wildcard matches subdomain",
			hostname: "api.example.com",
			pattern:  "*.example.com",
			want:     true,
		},
		{
			name:     "suffix wildcard matches nested subdomain",
			hostname: "deep.nested.example.com",
			pattern:  "*.example.com",
			want:     true,
		},
		{
			name:     "suffix wildcard case insensitive",
			hostname: "API.EXAMPLE.COM",
			pattern:  "*.example.com",
			want:     true,
		},
		{
			name:     "suffix wildcard does not match exact domain",
			hostname: "example.com",
			pattern:  "*.example.com",
			want:     false,
		},
		{
			name:     "suffix wildcard does not match different domain",
			hostname: "api.other.com",
			pattern:  "*.example.com",
			want:     false,
		},
		{
			name:     "suffix wildcard does not match partial suffix",
			hostname: "notexample.com",
			pattern:  "*.example.com",
			want:     false,
		},

		// Edge cases
		{
			name:     "empty hostname",
			hostname: "",
			pattern:  "example.com",
			want:     false,
		},
		{
			name:     "empty pattern",
			hostname: "example.com",
			pattern:  "",
			want:     false,
		},
		{
			name:     "both empty - empty pattern never matches",
			hostname: "",
			pattern:  "",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchDomain(tt.hostname, tt.pattern)
			if got != tt.want {
				t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.hostname, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestIsEgressAllowed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		network  *orchestrator.SandboxNetworkConfig
		hostname string
		ip       net.IP
		dstPort  uint16
		want     bool
	}{
		// ---------------------------------------------------------------------
		// Default Allow Behavior
		// Traffic is allowed unless explicitly blocked by denied CIDRs.
		// ---------------------------------------------------------------------
		{
			name:     "nil network config allows all",
			network:  nil,
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     true,
		},
		{
			name:     "nil egress config allows all",
			network:  &orchestrator.SandboxNetworkConfig{},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     true,
		},
		{
			name: "empty egress config allows all",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{},
			},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     true,
		},

		// ---------------------------------------------------------------------
		// Denied CIDRs (The Only Blocking Mechanism)
		// This is the ONLY way to block traffic. Everything else is allowed.
		// ---------------------------------------------------------------------
		{
			name: "denied CIDR blocks traffic",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Denied: []string{"10.0.0.0/8"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("10.1.2.3"),
			want:     false,
		},
		{
			name: "denied CIDR exact IP blocks",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Denied: []string{"1.2.3.4/32"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("1.2.3.4"),
			want:     false,
		},
		{
			name: "IP not in denied CIDR allows",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Denied: []string{"10.0.0.0/8"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("192.168.1.1"),
			want:     true,
		},

		// ---------------------------------------------------------------------
		// Whitelist Mode: Deny All + Bypass Exceptions
		// ---------------------------------------------------------------------
		{
			name: "whitelist mode: deny all with domain bypass",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"example.com"},
					Denied:  []string{"0.0.0.0/0"}, // Required to block by default
				},
			},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     true, // Domain bypass checked before denied CIDRs
		},
		{
			name: "whitelist mode: deny all with CIDR bypass",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"10.0.0.0/8"},
					Denied:  []string{"0.0.0.0/0"}, // Required to block by default
				},
			},
			hostname: "",
			ip:       net.ParseIP("10.1.2.3"),
			want:     true, // CIDR bypass checked before denied CIDRs
		},
		{
			name: "whitelist mode: traffic blocked when no bypass matches",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"example.com"},
					Denied:  []string{"0.0.0.0/0"},
				},
			},
			hostname: "other.com", // Domain doesn't match bypass
			ip:       net.ParseIP("1.2.3.4"),
			want:     false, // Blocked by denied CIDR (0.0.0.0/0)
		},

		// ---------------------------------------------------------------------
		// Bypass Rules Always Win Over Deny
		// ---------------------------------------------------------------------
		{
			name: "bypass: broad allowed CIDR bypasses specific denied CIDR",
			// Warning: A broad allow will bypass a more specific deny.
			// IP 10.1.1.1 matches allowed 10.0.0.0/8 -> bypass, never checks denied.
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"10.0.0.0/8"},
					Denied:  []string{"10.1.1.1/32"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("10.1.1.1"),
			want:     true, // Bypass matched, deny never checked
		},
		{
			name: "bypass: specific allowed CIDR bypasses broad denied CIDR",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"10.1.1.1/32"},
					Denied:  []string{"10.0.0.0/8"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("10.1.1.1"),
			want:     true, // Bypass matched
		},
		{
			name: "bypass: domain bypass skips denied CIDR check entirely",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"example.com"},
					Denied:  []string{sandbox_network.AllInternetTrafficCIDR},
				},
			},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     true,
		},
		{
			name: "no bypass match: denied CIDR blocks traffic",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"allowed.com", "192.168.0.0/16"},
					Denied:  []string{sandbox_network.AllInternetTrafficCIDR},
				},
			},
			hostname: "other.com",
			ip:       net.ParseIP("10.1.2.3"),
			want:     false,
		},

		// ---------------------------------------------------------------------
		// Multiple Rules
		// ---------------------------------------------------------------------
		{
			name: "multiple allowed domains second matches",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"first.com", "second.com", "third.com"},
					Denied:  []string{sandbox_network.AllInternetTrafficCIDR},
				},
			},
			hostname: "second.com",
			ip:       net.ParseIP("1.2.3.4"),
			want:     true,
		},
		{
			name: "multiple allowed CIDRs second matches",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
					Denied:  []string{sandbox_network.AllInternetTrafficCIDR},
				},
			},
			hostname: "",
			ip:       net.ParseIP("172.20.1.1"),
			want:     true,
		},
		{
			name: "multiple denied CIDRs second matches",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Denied: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("172.20.1.1"),
			want:     false,
		},

		// ---------------------------------------------------------------------
		// Port-Specific Rules
		// ---------------------------------------------------------------------
		{
			name: "allowed CIDR with matching port",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"8.8.8.8:53"},
					Denied:  []string{"0.0.0.0/0"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  53,
			want:     true,
		},
		{
			name: "allowed CIDR with non-matching port",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"8.8.8.8:53"},
					Denied:  []string{"0.0.0.0/0"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  80,
			want:     false, // Port 80 not in allowed range, falls through to deny-all
		},
		{
			name: "allowed CIDR with port range",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"8.8.8.8:80-443"},
					Denied:  []string{"0.0.0.0/0"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  443,
			want:     true,
		},
		{
			name: "allowed CIDR with port range excludes out-of-range",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"8.8.8.8:80-443"},
					Denied:  []string{"0.0.0.0/0"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  8080,
			want:     false,
		},
		{
			name: "denied CIDR with matching port",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Denied: []string{"8.8.8.8:80"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  80,
			want:     false,
		},
		{
			name: "denied CIDR with non-matching port allows",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Denied: []string{"8.8.8.8:80"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  443,
			want:     true, // Port 443 not in deny range, default allow
		},
		{
			name: "allowed domain with matching port",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"example.com:443"},
					Denied:  []string{"0.0.0.0/0"},
				},
			},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			dstPort:  443,
			want:     true,
		},
		{
			name: "allowed domain with non-matching port",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"example.com:443"},
					Denied:  []string{"0.0.0.0/0"},
				},
			},
			hostname: "example.com",
			ip:       net.ParseIP("1.2.3.4"),
			dstPort:  80,
			want:     false, // Domain matches but port doesn't
		},
		{
			name: "all-ports allow wins over port-specific deny (same IP)",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"8.8.8.8"},
					Denied:  []string{"8.8.8.8:80"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  80,
			want:     true, // All-ports allow checked before port-specific deny
		},
		{
			name: "port-specific allow with all-ports deny",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"8.8.8.8:53"},
					Denied:  []string{"8.8.8.8"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  53,
			want:     true, // Port-specific allow checked before all-ports deny
		},
		{
			name: "port-specific allow does not cover other ports",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"8.8.8.8:53"},
					Denied:  []string{"8.8.8.8"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("8.8.8.8"),
			dstPort:  80,
			want:     false, // Port 80 not in allow, falls to all-ports deny
		},

		// ---------------------------------------------------------------------
		// Error Handling
		// ---------------------------------------------------------------------
		{
			name: "non-IP allowed entry treated as domain, no error",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					Allowed: []string{"invalid-cidr"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("1.2.3.4"),
			want:     true, // Treated as domain, no hostname match, default allow
		},
		// Domain entries in deny are rejected at parse time (NewEgressACL).
		// See TestNewEgressACL_RejectsDomainInDeny in rule_test.go.
	}

	const portNotRelevant uint16 = 6666

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sbxConfig, err := sandbox.NewConfig(sandbox.Config{Network: tt.network})
			require.NoError(t, err)
			sbx := &sandbox.Sandbox{
				Metadata: &sandbox.Metadata{
					Config: sbxConfig,
				},
			}

			dstPort := tt.dstPort
			if dstPort == 0 {
				dstPort = portNotRelevant
			}

			got, _ := isEgressAllowed(sbx, tt.hostname, tt.ip, dstPort)

			if got != tt.want {
				t.Errorf("isEgressAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAlwaysDeniedCIDRs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		// IPs in denied CIDRs (internal/private ranges)
		{"10.0.0.1 is denied", "10.0.0.1", true},
		{"10.255.255.255 is denied", "10.255.255.255", true},
		{"192.168.1.1 is denied", "192.168.1.1", true},
		{"172.16.0.1 is denied", "172.16.0.1", true},
		{"172.31.255.255 is denied", "172.31.255.255", true},
		{"169.254.1.1 is denied (link-local)", "169.254.1.1", true},
		{"127.0.0.1 is denied (loopback)", "127.0.0.1", true},

		// IPs NOT in denied CIDRs (public IPs)
		{"8.8.8.8 is allowed (Google DNS)", "8.8.8.8", false},
		{"1.1.1.1 is allowed (Cloudflare)", "1.1.1.1", false},
		{"142.250.80.46 is allowed (Google)", "142.250.80.46", false},

		// IPv6 denied ranges
		{"::1 is denied (IPv6 loopback)", "::1", true},
		{"fc00::1 is denied (IPv6 unique local)", "fc00::1", true},
		{"fe80::1 is denied (IPv6 link-local)", "fe80::1", true},

		// IPv6 allowed (public)
		{"2001:4860:4860::8888 is allowed (Google IPv6 DNS)", "2001:4860:4860::8888", false},

		// Edge cases around CIDR boundaries
		{"172.15.255.255 is allowed (just before 172.16.0.0/12)", "172.15.255.255", false},
		{"172.32.0.0 is allowed (just after 172.16.0.0/12)", "172.32.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("Failed to parse IP: %s", tt.ip)
			}

			got := isIPInAlwaysDeniedCIDRs(ip)
			if got != tt.want {
				t.Errorf("isIPInDeniedCIDRs(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
