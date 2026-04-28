package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"

	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

func boolPtr(b bool) *bool { return &b }

func TestIsNonEnvdTrafficRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		md       metadata.MD
		expected bool
	}{
		{
			name:     "no port metadata returns true",
			md:       metadata.MD{},
			expected: true,
		},
		{
			name:     "envd port returns false",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "49983"),
			expected: false,
		},
		{
			name:     "non-envd port returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "8080"),
			expected: true,
		},
		{
			name:     "invalid port string returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "not-a-number"),
			expected: true,
		},
		{
			name:     "empty port string returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, ""),
			expected: true,
		},
		{
			name:     "port 0 returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "0"),
			expected: true,
		},
		{
			name:     "port 443 returns true",
			md:       metadata.Pairs(proxygrpc.MetadataSandboxRequestPort, "443"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isNonEnvdTrafficRequest(context.Background(), tt.md, "test-sandbox")
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsPrivateIngressTraffic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		network  *dbtypes.SandboxNetworkConfig
		expected bool
	}{
		{
			name:     "nil network",
			network:  nil,
			expected: false,
		},
		{
			name:     "nil ingress",
			network:  &dbtypes.SandboxNetworkConfig{Ingress: nil},
			expected: false,
		},
		{
			name:     "nil AllowPublicAccess",
			network:  &dbtypes.SandboxNetworkConfig{Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: nil}},
			expected: false,
		},
		{
			name:     "AllowPublicAccess true",
			network:  &dbtypes.SandboxNetworkConfig{Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: boolPtr(true)}},
			expected: false,
		},
		{
			name:     "AllowPublicAccess false",
			network:  &dbtypes.SandboxNetworkConfig{Ingress: &dbtypes.SandboxNetworkIngressConfig{AllowPublicAccess: boolPtr(false)}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isPrivateIngressTraffic(tt.network)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTokensMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provided string
		expected string
		match    bool
	}{
		{
			name:     "identical tokens match",
			provided: "secret-token-123",
			expected: "secret-token-123",
			match:    true,
		},
		{
			name:     "different tokens do not match",
			provided: "wrong-token",
			expected: "secret-token-123",
			match:    false,
		},
		{
			name:     "empty provided does not match",
			provided: "",
			expected: "secret-token-123",
			match:    false,
		},
		{
			name:     "empty expected does not match",
			provided: "secret-token-123",
			expected: "",
			match:    false,
		},
		{
			name:     "both empty match",
			provided: "",
			expected: "",
			match:    true,
		},
		{
			name:     "different length tokens do not match",
			provided: "short",
			expected: "much-longer-token-value",
			match:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tokensMatch(tt.provided, tt.expected)
			assert.Equal(t, tt.match, result)
		})
	}
}

func TestValidateClientProxyAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		md    metadata.MD
		orgID string
	}{
		{
			name: "denies when auth org is not configured",
			md:   metadata.MD{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateClientProxyAuth(context.Background(), tt.md, nil, tt.orgID)
			assert.Error(t, err)
		})
	}
}

func TestRequireBearerMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		md      metadata.MD
		wantErr bool
	}{
		{
			name:    "missing authorization",
			md:      metadata.MD{},
			wantErr: true,
		},
		{
			name:    "malformed authorization",
			md:      metadata.Pairs(proxygrpc.MetadataAuthorization, "token"),
			wantErr: true,
		},
		{
			name:    "empty bearer token",
			md:      metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer "),
			wantErr: true,
		},
		{
			name: "bearer token",
			md:   metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := requireBearerMetadata(tt.md)
			if tt.wantErr {
				assert.Error(t, err)

				return
			}
			assert.NoError(t, err)
		})
	}
}

type fakeClientProxyOAuthVerifier struct {
	claims ClientProxyOAuthClaims
	err    error
}

func (v fakeClientProxyOAuthVerifier) VerifyClaims(context.Context, string) (ClientProxyOAuthClaims, error) {
	return v.claims, v.err
}

func TestValidateClientProxyAuthOAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		md            metadata.MD
		verifier      ClientProxyOAuthVerifier
		expectedOrgID string
		wantErr       bool
	}{
		{
			name:          "valid bearer token org",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier:      fakeClientProxyOAuthVerifier{claims: ClientProxyOAuthClaims{Subject: "client_123", OrgID: "org_123"}},
			expectedOrgID: "org_123",
		},
		{
			name:          "missing bearer token",
			md:            metadata.MD{},
			verifier:      fakeClientProxyOAuthVerifier{claims: ClientProxyOAuthClaims{Subject: "client_123", OrgID: "org_123"}},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
		{
			name:          "wrong bearer token org",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier:      fakeClientProxyOAuthVerifier{claims: ClientProxyOAuthClaims{Subject: "client_123", OrgID: "org_456"}},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
		{
			name:          "missing org claim",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier:      fakeClientProxyOAuthVerifier{claims: ClientProxyOAuthClaims{Subject: "client_123"}},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
		{
			name:          "verifier error",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier:      fakeClientProxyOAuthVerifier{err: errors.New("invalid token")},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
		{
			name:          "configured auth org requires verifier",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			expectedOrgID: "org_123",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateClientProxyAuth(context.Background(), tt.md, tt.verifier, tt.expectedOrgID)
			if tt.wantErr {
				assert.Error(t, err)

				return
			}
			assert.NoError(t, err)
		})
	}
}
