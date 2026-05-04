package oidc

import (
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	jose "github.com/go-jose/go-jose/v4"
)

// NewTestServer starts a TLS server that exposes both an OIDC discovery
// endpoint and a JWKS endpoint signed with publicKey. The discovery doc
// reports `discoveryIssuer` as its issuer; tests can use this to simulate
// matching or mismatched issuer values.
//
// This helper is exported so tests in sibling packages can construct an OIDC
// fixture without duplicating the boilerplate.
func NewTestServer(t *testing.T, publicKey *rsa.PublicKey, keyID string, discoveryIssuer string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		err := json.NewEncoder(w).Encode(map[string]string{
			"issuer":   discoveryIssuer,
			"jwks_uri": server.URL + "/jwks",
		})
		if err != nil {
			t.Errorf("encode discovery response: %v", err)
		}
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		err := json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{
				Key:       publicKey,
				KeyID:     keyID,
				Algorithm: string(jose.RS256),
				Use:       "sig",
			},
		}})
		if err != nil {
			t.Errorf("encode JWKS response: %v", err)
		}
	})

	return server
}
