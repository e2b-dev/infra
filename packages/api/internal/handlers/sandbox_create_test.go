package handlers

import (
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

func TestValidateNetworkConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		network    *api.SandboxNetworkConfig
		wantErr    bool
		wantCode   int
		wantErrMsg string
	}{
		{
			name:    "nil network config is valid",
			network: nil,
			wantErr: false,
		},
		{
			name:    "empty network config is valid",
			network: &api.SandboxNetworkConfig{},
			wantErr: false,
		},
		{
			name: "valid deny_out with CIDR",
			network: &api.SandboxNetworkConfig{
				DenyOut: &[]string{"10.0.0.0/8"},
			},
			wantErr: false,
		},
		{
			name: "invalid deny_out entry",
			network: &api.SandboxNetworkConfig{
				DenyOut: &[]string{"not-a-cidr"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: "invalid denied CIDR not-a-cidr",
		},
		// Domain validation tests
		{
			name: "allow_out with domain requires deny_out block-all",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: ErrMsgDomainsRequireBlockAll,
		},
		{
			name: "allow_out with domain and block-all deny_out is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
		{
			name: "allow_out with domain and partial deny_out is invalid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com"},
				DenyOut:  &[]string{"10.0.0.0/8"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: ErrMsgDomainsRequireBlockAll,
		},
		{
			name: "allow_out with wildcard domain requires deny_out block-all",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"*.example.com"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: ErrMsgDomainsRequireBlockAll,
		},
		{
			name: "allow_out with wildcard domain and block-all deny_out is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"*.example.com"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
		// CIDR validation tests
		{
			name: "allow_out with CIDR without deny_out is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8"},
			},
			wantErr: false,
		},
		{
			name: "allow_out with CIDR and deny_out block-all is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
		{
			name: "allow_out with IP without deny_out is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"8.8.8.8"},
			},
			wantErr: false,
		},
		{
			name: "allow_out with IP and deny_out block-all is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"8.8.8.8"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
		// CIDR intersection validation tests
		{
			name: "allow_out CIDR not covered by deny_out CIDR is valid (no intersection check)",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8"},
				DenyOut:  &[]string{"192.168.0.0/16"}, // No intersection, but still valid
			},
			wantErr: false,
		},
		{
			name: "allow_out CIDR covered by intersecting deny_out CIDR is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.1.0.0/16"},
				DenyOut:  &[]string{"10.0.0.0/8"}, // Deny covers allow
			},
			wantErr: false,
		},
		{
			name: "allow_out CIDR covers deny_out CIDR is valid (intersection exists)",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8"},
				DenyOut:  &[]string{"10.1.0.0/16"}, // Allow covers deny - still valid intersection
			},
			wantErr: false,
		},
		{
			name: "allow_out IP covered by deny_out CIDR is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.1.2.3"},
				DenyOut:  &[]string{"10.0.0.0/8"},
			},
			wantErr: false,
		},
		{
			name: "allow_out IP not covered by deny_out CIDR is valid (no intersection check)",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"8.8.8.8"},
				DenyOut:  &[]string{"10.0.0.0/8"},
			},
			wantErr: false,
		},
		{
			name: "multiple allow_out CIDRs partial deny_out coverage is valid (no intersection check)",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8", "192.168.0.0/16"},
				DenyOut:  &[]string{"10.0.0.0/8"}, // Only covers first, but still valid
			},
			wantErr: false,
		},
		{
			name: "multiple allow_out CIDRs covered by multiple deny_out CIDRs is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"10.0.0.0/8", "192.168.0.0/16"},
				DenyOut:  &[]string{"10.0.0.0/8", "192.168.0.0/16"},
			},
			wantErr: false,
		},
		// Mixed domain and CIDR tests
		{
			name: "allow_out with domain and CIDR without deny_out block-all is invalid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com", "8.8.8.8"},
				DenyOut:  &[]string{"10.0.0.0/8"},
			},
			wantErr:    true,
			wantCode:   http.StatusBadRequest,
			wantErrMsg: ErrMsgDomainsRequireBlockAll,
		},
		{
			name: "allow_out with domain and CIDR with deny_out block-all is valid",
			network: &api.SandboxNetworkConfig{
				AllowOut: &[]string{"example.com", "8.8.8.8"},
				DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateNetworkConfig(tt.network)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateNetworkConfig() expected error, got nil")

					return
				}

				if err.Code != tt.wantCode {
					t.Errorf("validateNetworkConfig() error code = %v, want %v", err.Code, tt.wantCode)
				}

				if err.ClientMsg != tt.wantErrMsg {
					t.Errorf("validateNetworkConfig() error message = %q, want %q", err.ClientMsg, tt.wantErrMsg)
				}
			} else if err != nil {
				t.Errorf("validateNetworkConfig() unexpected error: %v", err)
			}
		})
	}
}
