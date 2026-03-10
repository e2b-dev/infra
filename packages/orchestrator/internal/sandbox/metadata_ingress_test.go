package sandbox

import (
	"net"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestIngressClientCIDRs_NilIngress(t *testing.T) {
	t.Parallel()

	m := &Metadata{}
	if m.HasIngressClientCIDRs() {
		t.Error("HasIngressClientCIDRs() = true for nil ingress, want false")
	}
	if m.IngressAllowsClientIP(net.ParseIP("1.2.3.4")) {
		t.Error("IngressAllowsClientIP() = true for nil ingress, want false")
	}
	if m.IngressDeniesClientIP(net.ParseIP("1.2.3.4")) {
		t.Error("IngressDeniesClientIP() = true for nil ingress, want false")
	}
}

func TestIngressClientCIDRs_AllowDenyPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		allowed     []string
		denied      []string
		ip          string
		wantAllowed bool
		wantDenied  bool
	}{
		{
			name:        "IP in allowed CIDR",
			allowed:     []string{"10.0.0.0/8"},
			ip:          "10.1.2.3",
			wantAllowed: true,
			wantDenied:  false,
		},
		{
			name:       "IP in denied CIDR",
			denied:     []string{"10.0.0.0/8"},
			ip:         "10.1.2.3",
			wantDenied: true,
		},
		{
			name:        "IP in both allowed and denied — both report true",
			allowed:     []string{"10.0.0.0/8"},
			denied:      []string{"10.1.0.0/16"},
			ip:          "10.1.2.3",
			wantAllowed: true,
			wantDenied:  true,
		},
		{
			name: "IP in neither",
			allowed: []string{"192.168.0.0/16"},
			denied:  []string{"172.16.0.0/12"},
			ip:      "10.1.2.3",
		},
		{
			name:        "bare IP allowed",
			allowed:     []string{"1.2.3.4"},
			ip:          "1.2.3.4",
			wantAllowed: true,
		},
		{
			name:       "bare IP denied",
			denied:     []string{"1.2.3.4"},
			ip:         "1.2.3.4",
			wantDenied: true,
		},
		{
			name:    "bare IP no match",
			allowed: []string{"1.2.3.4"},
			ip:      "1.2.3.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &Metadata{}
			m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{
				AllowedClientCidrs: tt.allowed,
				DeniedClientCidrs:  tt.denied,
			})

			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP: %s", tt.ip)
			}

			if got := m.IngressAllowsClientIP(ip); got != tt.wantAllowed {
				t.Errorf("IngressAllowsClientIP(%s) = %v, want %v", tt.ip, got, tt.wantAllowed)
			}
			if got := m.IngressDeniesClientIP(ip); got != tt.wantDenied {
				t.Errorf("IngressDeniesClientIP(%s) = %v, want %v", tt.ip, got, tt.wantDenied)
			}
		})
	}
}

func TestIngressClientCIDRs_SetNetworkIngress_Replaces(t *testing.T) {
	t.Parallel()

	m := &Metadata{}
	m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{
		AllowedClientCidrs: []string{"10.0.0.0/8"},
	})

	if !m.IngressAllowsClientIP(net.ParseIP("10.1.2.3")) {
		t.Fatal("expected 10.1.2.3 to be allowed initially")
	}

	// Replace with different config.
	m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{
		DeniedClientCidrs: []string{"10.0.0.0/8"},
	})

	if m.IngressAllowsClientIP(net.ParseIP("10.1.2.3")) {
		t.Error("expected 10.1.2.3 to NOT be allowed after replacement")
	}
	if !m.IngressDeniesClientIP(net.ParseIP("10.1.2.3")) {
		t.Error("expected 10.1.2.3 to be denied after replacement")
	}
}

func TestIngressClientCIDRs_SetNil_Clears(t *testing.T) {
	t.Parallel()

	m := &Metadata{}
	m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{
		AllowedClientCidrs: []string{"10.0.0.0/8"},
	})

	if !m.HasIngressClientCIDRs() {
		t.Fatal("expected HasIngressClientCIDRs() = true")
	}

	m.SetNetworkIngress(nil)

	if m.HasIngressClientCIDRs() {
		t.Error("expected HasIngressClientCIDRs() = false after setting nil")
	}
}
