package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

func TestNewVerifierConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		issuerURL string
		audience  string
		wantErr   bool
	}{
		{
			name: "empty config returns noop verifier",
		},
		{
			name:      "missing audience",
			issuerURL: "https://issuer.example.com",
			wantErr:   true,
		},
		{
			name:     "missing issuer",
			audience: "client-proxy",
			wantErr:  true,
		},
		{
			name:      "trims empty config",
			issuerURL: " ",
			audience:  "\t",
		},
		{
			name:      "trims partial config",
			issuerURL: " https://issuer.example.com ",
			audience:  " ",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			verifier, err := NewVerifier(context.Background(), tt.issuerURL, tt.audience)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			require.NotNil(t, verifier)
		})
	}
}

func TestNoopVerifierRejectsClaims(t *testing.T) {
	t.Parallel()

	verifier, err := NewVerifier(context.Background(), "", "")
	require.NoError(t, err)

	claims, err := verifier.VerifyClaims(context.Background(), "token")
	require.Error(t, err)
	require.Empty(t, claims)
}

func TestNewVerifierLoadsOIDCProvider(t *testing.T) {
	t.Parallel()

	issuerURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(t, w, map[string]string{
				"issuer":   issuerURL,
				"jwks_uri": issuerURL + "/jwks",
			})
		case "/jwks":
			writeJSON(t, w, map[string]any{"keys": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	issuerURL = server.URL

	verifier, err := NewVerifier(context.Background(), server.URL, "client-proxy")
	require.NoError(t, err)
	require.NotNil(t, verifier)
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}

type fakeVerifier struct {
	claims Claims
	err    error
}

func (v fakeVerifier) VerifyClaims(context.Context, string) (Claims, error) {
	return v.claims, v.err
}

func TestRequireBearer(t *testing.T) {
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

			err := RequireBearer(tt.md)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRequireOrg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		md            metadata.MD
		verifier      Verifier
		expectedOrgID string
		wantErr       bool
	}{
		{
			name:          "valid bearer token org",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier:      fakeVerifier{claims: Claims{Subject: "client_123", OrgID: "org_123"}},
			expectedOrgID: "org_123",
		},
		{
			name:          "missing bearer token",
			md:            metadata.MD{},
			verifier:      fakeVerifier{claims: Claims{Subject: "client_123", OrgID: "org_123"}},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
		{
			name:          "wrong bearer token org",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier:      fakeVerifier{claims: Claims{Subject: "client_123", OrgID: "org_456"}},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
		{
			name:          "missing org claim",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier:      fakeVerifier{claims: Claims{Subject: "client_123"}},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
		{
			name:          "verifier error",
			md:            metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier:      fakeVerifier{err: errors.New("invalid token")},
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

			err := RequireOrg(context.Background(), tt.md, tt.verifier, tt.expectedOrgID)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRequireClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		md       metadata.MD
		verifier Verifier
		want     Claims
		wantErr  bool
	}{
		{
			name:     "valid bearer token",
			md:       metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier: fakeVerifier{claims: Claims{Subject: "client_123", OrgID: "org_123"}},
			want:     Claims{Subject: "client_123", OrgID: "org_123"},
		},
		{
			name:     "missing bearer token",
			md:       metadata.MD{},
			verifier: fakeVerifier{claims: Claims{Subject: "client_123", OrgID: "org_123"}},
			wantErr:  true,
		},
		{
			name:    "configured auth requires verifier",
			md:      metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			wantErr: true,
		},
		{
			name:     "verifier error",
			md:       metadata.Pairs(proxygrpc.MetadataAuthorization, "Bearer token"),
			verifier: fakeVerifier{err: errors.New("invalid token")},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			claims, err := RequireClaims(context.Background(), tt.md, tt.verifier)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, claims)
		})
	}
}

func TestRequireOrgClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		claims        Claims
		expectedOrgID string
		wantErr       bool
	}{
		{
			name:          "matching org",
			claims:        Claims{OrgID: "org_123"},
			expectedOrgID: "org_123",
		},
		{
			name:          "trims expected org",
			claims:        Claims{OrgID: "org_123"},
			expectedOrgID: " org_123 ",
		},
		{
			name:          "wrong org",
			claims:        Claims{OrgID: "org_456"},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
		{
			name:          "missing expected org",
			claims:        Claims{OrgID: "org_123"},
			expectedOrgID: "",
			wantErr:       true,
		},
		{
			name:          "missing claim org",
			claims:        Claims{},
			expectedOrgID: "org_123",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := RequireOrgClaims(tt.claims, tt.expectedOrgID)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
		})
	}
}
