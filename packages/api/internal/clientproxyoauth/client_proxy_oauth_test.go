package clientproxyoauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
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
