package sandbox

import (
	"net"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestIngress_IsIngressAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		allowed []string
		denied  []string
		ip      string
		port    uint16
		want    bool
	}{
		{
			name: "no rules — default allow",
			ip:   "10.1.2.3",
			port: 8080,
			want: true,
		},
		{
			name:    "IP in allowed CIDR — allowed",
			allowed: []string{"10.0.0.0/8"},
			ip:      "10.1.2.3",
			port:    8080,
			want:    true,
		},
		{
			name:   "IP in denied CIDR — denied",
			denied: []string{"10.0.0.0/8"},
			ip:     "10.1.2.3",
			port:   8080,
			want:   false,
		},
		{
			name:    "IP in both allowed and denied — allow wins",
			allowed: []string{"10.0.0.0/8"},
			denied:  []string{"10.1.0.0/16"},
			ip:      "10.1.2.3",
			port:    8080,
			want:    true,
		},
		{
			name:    "IP in neither — default allow",
			allowed: []string{"192.168.0.0/16"},
			denied:  []string{"172.16.0.0/12"},
			ip:      "10.1.2.3",
			port:    8080,
			want:    true,
		},
		{
			name:    "bare IP allowed",
			allowed: []string{"1.2.3.4"},
			ip:      "1.2.3.4",
			port:    8080,
			want:    true,
		},
		{
			name:   "bare IP denied",
			denied: []string{"1.2.3.4"},
			ip:     "1.2.3.4",
			port:   8080,
			want:   false,
		},
		{
			name:    "bare IP no match — default allow",
			allowed: []string{"1.2.3.4"},
			ip:      "1.2.3.5",
			port:    8080,
			want:    true,
		},
		// Port-specific rules
		{
			name:   "deny specific port — port matches",
			denied: []string{"0.0.0.0/0:8080"},
			ip:     "10.1.2.3",
			port:   8080,
			want:   false,
		},
		{
			name:   "deny specific port — port does not match",
			denied: []string{"0.0.0.0/0:8080"},
			ip:     "10.1.2.3",
			port:   9090,
			want:   true,
		},
		{
			name:    "allow specific port overrides deny-all",
			allowed: []string{"0.0.0.0/0:8080"},
			denied:  []string{"0.0.0.0/0"},
			ip:      "10.1.2.3",
			port:    8080,
			want:    true,
		},
		{
			name:    "allow specific port — other port still denied",
			allowed: []string{"0.0.0.0/0:8080"},
			denied:  []string{"0.0.0.0/0"},
			ip:      "10.1.2.3",
			port:    9090,
			want:    false,
		},
		// Port range rules
		{
			name:    "allow port range — port in range",
			allowed: []string{"0.0.0.0/0:80-443"},
			denied:  []string{"0.0.0.0/0"},
			ip:      "10.1.2.3",
			port:    443,
			want:    true,
		},
		{
			name:    "allow port range — port out of range",
			allowed: []string{"0.0.0.0/0:80-443"},
			denied:  []string{"0.0.0.0/0"},
			ip:      "10.1.2.3",
			port:    8080,
			want:    false,
		},
		// Combined IP + port
		{
			name:   "deny narrow CIDR + port — both match",
			denied: []string{"10.1.0.0/16:8080"},
			ip:     "10.1.2.3",
			port:   8080,
			want:   false,
		},
		{
			name:   "deny narrow CIDR + port — IP matches, port doesn't",
			denied: []string{"10.1.0.0/16:8080"},
			ip:     "10.1.2.3",
			port:   9090,
			want:   true,
		},
		{
			name:   "deny narrow CIDR + port — port matches, IP doesn't",
			denied: []string{"10.1.0.0/16:8080"},
			ip:     "192.168.1.1",
			port:   8080,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &Metadata{}
			m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{
				Allowed: tt.allowed,
				Denied:  tt.denied,
			})

			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP: %s", tt.ip)
			}

			if got := m.IsIngressAllowed(ip, tt.port); got != tt.want {
				t.Errorf("IsIngressAllowed(%s, %d) = %v, want %v", tt.ip, tt.port, got, tt.want)
			}
		})
	}
}

func TestIngress_SetNetworkIngress_Replaces(t *testing.T) {
	t.Parallel()

	m := &Metadata{}
	m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{
		Allowed: []string{"10.0.0.0/8"},
	})

	if !m.IsIngressAllowed(net.ParseIP("10.1.2.3"), 8080) {
		t.Fatal("expected 10.1.2.3:8080 to be allowed initially")
	}

	// Replace with different config.
	m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{
		Denied: []string{"10.0.0.0/8"},
	})

	if m.IsIngressAllowed(net.ParseIP("10.1.2.3"), 8080) {
		t.Error("expected 10.1.2.3:8080 to be denied after replacement")
	}
}

func TestIngress_HasIngressRules(t *testing.T) {
	t.Parallel()

	m := &Metadata{}
	if m.HasIngressRules() {
		t.Error("expected no rules initially")
	}

	m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{
		Denied: []string{"10.0.0.0/8"},
	})

	if !m.HasIngressRules() {
		t.Error("expected rules after SetNetworkIngress")
	}

	m.SetNetworkIngress(&orchestrator.SandboxNetworkIngressConfig{})

	if m.HasIngressRules() {
		t.Error("expected no rules after clearing")
	}
}
