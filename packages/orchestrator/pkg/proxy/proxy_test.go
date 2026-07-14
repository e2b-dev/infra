//go:build linux

package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestSchemeForPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ingress *orchestrator.SandboxNetworkIngressConfig
		port    uint64
		want    string
	}{
		{
			name:    "nil ingress defaults to http",
			ingress: nil,
			port:    8080,
			want:    "http",
		},
		{
			name:    "no HTTPS ports defaults to http",
			ingress: &orchestrator.SandboxNetworkIngressConfig{},
			port:    8080,
			want:    "http",
		},
		{
			name:    "port in HTTPS ports uses https",
			ingress: &orchestrator.SandboxNetworkIngressConfig{HttpsPorts: []uint32{443, 8443}},
			port:    8443,
			want:    "https",
		},
		{
			name:    "port not in HTTPS ports uses http",
			ingress: &orchestrator.SandboxNetworkIngressConfig{HttpsPorts: []uint32{443, 8443}},
			port:    8080,
			want:    "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, schemeForPort(tt.ingress, tt.port))
		})
	}
}
