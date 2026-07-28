package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/e2b-dev/infra/packages/shared/pkg/gcpauth"
)

type proxySequenceTokenSource struct {
	tokens []*oauth2.Token
	err    error
	calls  int
}

func (s *proxySequenceTokenSource) Token() (*oauth2.Token, error) {
	if s.err != nil {
		return nil, s.err
	}

	token := s.tokens[s.calls]
	s.calls++

	return token, nil
}

func TestSetRegistryAuthorizationRefreshesExpiredADC(t *testing.T) {
	source := &proxySequenceTokenSource{tokens: []*oauth2.Token{
		{AccessToken: "first", Expiry: time.Now().Add(-time.Minute)},
		{AccessToken: "second", Expiry: time.Now().Add(time.Hour)},
	}}
	store := &APIStore{registryAuth: gcpauth.NewRegistryAuthenticatorWithTokenSource(source)}

	first := httptest.NewRequestWithContext(t.Context(), "GET", "/v2/image/manifests/latest", nil)
	require.NoError(t, store.setRegistryAuthorization(first))
	require.Equal(t, "Bearer first", first.Header.Get("Authorization"))

	second := httptest.NewRequestWithContext(t.Context(), "GET", "/v2/image/manifests/latest", nil)
	require.NoError(t, store.setRegistryAuthorization(second))
	require.Equal(t, "Bearer second", second.Header.Get("Authorization"))
	require.Equal(t, 2, source.calls)
}

func TestSetRegistryAuthorizationFailsClosed(t *testing.T) {
	t.Run("missing authenticator", func(t *testing.T) {
		store := &APIStore{}
		request := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)

		require.ErrorContains(t, store.setRegistryAuthorization(request), "nil")
		require.Empty(t, request.Header.Get("Authorization"))
	})

	t.Run("token refresh failure", func(t *testing.T) {
		source := &proxySequenceTokenSource{err: errors.New("metadata unavailable")}
		store := &APIStore{registryAuth: gcpauth.NewRegistryAuthenticatorWithTokenSource(source)}
		request := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)

		require.ErrorContains(t, store.setRegistryAuthorization(request), "metadata unavailable")
		require.Empty(t, request.Header.Get("Authorization"))
	})
}

func TestProxyUploadAddsCurrentADCBeforeForwarding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer upload-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(upstream.Close)

	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)

	store := &APIStore{
		proxy: httputil.NewSingleHostReverseProxy(target),
		registryAuth: gcpauth.NewRegistryAuthenticatorWithTokenSource(
			oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "upload-token", Expiry: time.Now().Add(time.Hour)}),
		),
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/artifacts-uploads/upload-id", nil)
	response := httptest.NewRecorder()

	store.ProxyUpload(response, request)

	require.Equal(t, http.StatusAccepted, response.Code)
}
