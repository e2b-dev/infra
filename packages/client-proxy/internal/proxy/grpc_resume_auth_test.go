package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/metadata"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type stubTokenSource struct {
	token *oauth2.Token
	err   error
}

func (s stubTokenSource) Token() (*oauth2.Token, error) {
	return s.token, s.err
}

func TestNewGRPCResumeAuth(t *testing.T) {
	t.Parallel()

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()

		auth, err := newGrpcResumeAuth(context.Background(), GRPCOAuthConfig{})
		require.NoError(t, err)
		require.IsType(t, noopGrpcResumeAuth{}, auth)
	})

	t.Run("partial config", func(t *testing.T) {
		t.Parallel()

		auth, err := newGrpcResumeAuth(context.Background(), GRPCOAuthConfig{ClientID: "client-id"})
		require.Error(t, err)
		require.Nil(t, auth)
	})

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()

		auth, err := newGrpcResumeAuth(context.Background(), GRPCOAuthConfig{
			ClientID:     " client-id ",
			ClientSecret: " secret ",
			TokenURL:     " https://tokens.example.com ",
		})
		require.NoError(t, err)
		require.IsType(t, oauthGrpcResumeAuth{}, auth)
	})
}

func TestOAuthGrpcResumeAuthRequestsAutoresumeScope(t *testing.T) {
	t.Parallel()

	scopeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			errCh <- err
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		scopeCh <- r.Form.Get("scope")

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token",
			"token_type":   "bearer",
			"expires_in":   3600,
		}); err != nil {
			errCh <- err
		}
	}))
	t.Cleanup(server.Close)

	auth, err := newGrpcResumeAuth(context.Background(), GRPCOAuthConfig{
		ClientID:     "client-id",
		ClientSecret: "secret",
		TokenURL:     server.URL,
	})
	require.NoError(t, err)

	ctx, err := auth.authorize(context.Background())
	require.NoError(t, err)

	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)
	require.Equal(t, []string{"Bearer token"}, md.Get(proxygrpc.MetadataAuthorization))
	require.Equal(t, grpcResumeAuthScope, <-scopeCh)
	require.Empty(t, errCh)
}

func TestNoopGrpcResumeAuthAuthorize(t *testing.T) {
	t.Parallel()

	ctx, err := (noopGrpcResumeAuth{}).authorize(context.Background())
	require.NoError(t, err)

	md, ok := metadata.FromOutgoingContext(ctx)
	require.False(t, ok)
	require.Empty(t, md.Get(proxygrpc.MetadataAuthorization))
}

func TestOAuthGrpcResumeAuthAuthorize(t *testing.T) {
	t.Parallel()

	ctx, err := (oauthGrpcResumeAuth{tokenSource: stubTokenSource{token: &oauth2.Token{AccessToken: "token"}}}).authorize(context.Background())
	require.NoError(t, err)

	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)
	require.Equal(t, []string{"Bearer token"}, md.Get(proxygrpc.MetadataAuthorization))
}

func TestOAuthGrpcResumeAuthAuthorizeError(t *testing.T) {
	t.Parallel()

	tokenErr := errors.New("token failed")
	ctx, err := (oauthGrpcResumeAuth{tokenSource: stubTokenSource{err: tokenErr}}).authorize(context.Background())
	require.ErrorIs(t, err, tokenErr)

	md, ok := metadata.FromOutgoingContext(ctx)
	require.False(t, ok)
	require.Empty(t, md.Get(proxygrpc.MetadataAuthorization))
}
