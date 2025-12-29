package tcpfirewall

import (
	"context"
	"net"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	"inet.af/tcpproxy"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

func TestMatchDomain(t *testing.T) {
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
			got := matchDomain(tt.hostname, tt.pattern)
			if got != tt.want {
				t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.hostname, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestIsEgressAllowed(t *testing.T) {
	tests := []struct {
		name      string
		network   *orchestrator.SandboxNetworkConfig
		hostname  string
		ip        net.IP
		want      bool
		wantError bool
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
					DeniedCidrs: []string{"10.0.0.0/8"},
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
					DeniedCidrs: []string{"1.2.3.4/32"},
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
					DeniedCidrs: []string{"10.0.0.0/8"},
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
					AllowedDomains: []string{"example.com"},
					DeniedCidrs:    []string{"0.0.0.0/0"}, // Required to block by default
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
					AllowedCidrs: []string{"10.0.0.0/8"},
					DeniedCidrs:  []string{"0.0.0.0/0"}, // Required to block by default
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
					AllowedDomains: []string{"example.com"},
					DeniedCidrs:    []string{"0.0.0.0/0"},
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
					AllowedCidrs: []string{"10.0.0.0/8"},
					DeniedCidrs:  []string{"10.1.1.1/32"},
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
					AllowedCidrs: []string{"10.1.1.1/32"},
					DeniedCidrs:  []string{"10.0.0.0/8"},
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
					AllowedDomains: []string{"example.com"},
					DeniedCidrs:    []string{sandbox_network.AllInternetTrafficCIDR},
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
					AllowedDomains: []string{"allowed.com"},
					AllowedCidrs:   []string{"192.168.0.0/16"},
					DeniedCidrs:    []string{sandbox_network.AllInternetTrafficCIDR},
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
					AllowedDomains: []string{"first.com", "second.com", "third.com"},
					DeniedCidrs:    []string{sandbox_network.AllInternetTrafficCIDR},
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
					AllowedCidrs: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
					DeniedCidrs:  []string{sandbox_network.AllInternetTrafficCIDR},
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
					DeniedCidrs: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
				},
			},
			hostname: "",
			ip:       net.ParseIP("172.20.1.1"),
			want:     false,
		},

		// ---------------------------------------------------------------------
		// Error Handling
		// ---------------------------------------------------------------------
		{
			name: "invalid allowed CIDR returns error",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					AllowedCidrs: []string{"invalid-cidr"},
				},
			},
			hostname:  "",
			ip:        net.ParseIP("1.2.3.4"),
			want:      false,
			wantError: true,
		},
		{
			name: "invalid denied CIDR returns error",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					DeniedCidrs: []string{"not-a-cidr"},
				},
			},
			hostname:  "",
			ip:        net.ParseIP("1.2.3.4"),
			want:      false,
			wantError: true,
		},
		{
			name: "allowed CIDR checked before invalid denied CIDR",
			network: &orchestrator.SandboxNetworkConfig{
				Egress: &orchestrator.SandboxNetworkEgressConfig{
					AllowedCidrs: []string{"1.2.3.0/24"},
					DeniedCidrs:  []string{"invalid"},
				},
			},
			hostname:  "",
			ip:        net.ParseIP("1.2.3.4"),
			want:      true,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sbx := &sandbox.Sandbox{
				Metadata: &sandbox.Metadata{
					Config: sandbox.Config{
						Network: tt.network,
					},
				},
			}

			got, _, err := isEgressAllowed(sbx, tt.hostname, tt.ip)

			if tt.wantError {
				if err == nil {
					t.Errorf("isEgressAllowed() expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Errorf("isEgressAllowed() unexpected error: %v", err)

				return
			}

			if got != tt.want {
				t.Errorf("isEgressAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVerifyHostnameResolvesToIP(t *testing.T) {
	ctx := context.Background()
	nopLogger := logger.NewNopLogger()

	tests := []struct {
		name       string
		hostname   string
		expectedIP net.IP
		want       bool
	}{
		// Localhost tests - should work consistently
		{
			name:       "localhost resolves to 127.0.0.1",
			hostname:   "localhost",
			expectedIP: net.ParseIP("127.0.0.1"),
			want:       true,
		},
		{
			name:       "localhost does not resolve to random IP",
			hostname:   "localhost",
			expectedIP: net.ParseIP("8.8.8.8"),
			want:       false,
		},

		// Invalid hostname tests
		{
			name:       "non-existent domain fails lookup",
			hostname:   "this-domain-definitely-does-not-exist-12345.invalid",
			expectedIP: net.ParseIP("1.2.3.4"),
			want:       false,
		},
		{
			name:       "empty hostname fails lookup",
			hostname:   "",
			expectedIP: net.ParseIP("1.2.3.4"),
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := verifyHostnameResolvesToIP(ctx, nopLogger, tt.hostname, tt.expectedIP)
			if got != tt.want {
				t.Errorf("verifyHostnameResolvesToIP(%q, %v) = %v, want %v",
					tt.hostname, tt.expectedIP, got, tt.want)
			}
		})
	}
}

// TestDNSSpoofingPrevention_Integration tests the full DNS spoofing prevention flow.
// This simulates an attack where:
// 1. The firewall allows traffic to "google.com"
// 2. An attacker modifies /etc/hosts (or uses other DNS spoofing) to make google.com resolve to 1.1.1.1
// 3. The attacker connects to 1.1.1.1 claiming the hostname is "google.com"
// 4. The firewall should REJECT this because real DNS lookup shows google.com != 1.1.1.1
func TestDNSSpoofingPrevention_Integration(t *testing.T) {
	ctx := t.Context()
	nopLogger := logger.NewNopLogger()
	metrics := NewMetrics(noop.NewMeterProvider())

	// Scenario: Attacker spoofed DNS to make google.com point to 1.1.1.1 (Cloudflare's IP)
	// The client connects to 1.1.1.1 but claims hostname is "google.com"
	spoofedIP := net.ParseIP("1.1.1.1")
	claimedHostname := "google.com"

	// Create a sandbox with google.com in the allowlist
	sbx := &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.Config{
				Network: &orchestrator.SandboxNetworkConfig{
					Egress: &orchestrator.SandboxNetworkEgressConfig{
						AllowedDomains: []string{"google.com"},
						DeniedCidrs:    []string{sandbox_network.AllInternetTrafficCIDR}, // Deny all except allowed
					},
				},
			},
		},
	}

	// Create a mock connection using net.Pipe, wrapped in tcpproxy.Conn with the spoofed hostname
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	// Wrap in tcpproxy.Conn to provide the hostname (simulating TLS SNI or HTTP Host header)
	tcpConn := &tcpproxy.Conn{
		HostName: claimedHostname,
		Conn:     serverConn,
	}

	// Call domainHandler directly - this should close the connection due to DNS mismatch
	domainHandler(ctx, tcpConn, spoofedIP, 443, sbx, nopLogger, metrics, ProtocolTLS)

	// Verify the connection was closed by trying to write to it
	// Set a short deadline to avoid hanging
	clientConn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_, err := clientConn.Write([]byte("test"))

	if err == nil {
		t.Errorf("Expected connection to be closed due to DNS spoofing, but write succeeded")
	}

	t.Log("SUCCESS: DNS spoofing attack prevented via domainHandler")
	t.Log("  - Attacker connected to 1.1.1.1 claiming hostname google.com")
	t.Log("  - domainHandler detected DNS mismatch and closed the connection")
}
