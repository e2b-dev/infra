package proxy

import (
	"context"
	"errors"
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

func TestNewGrpcResumeAuth(t *testing.T) {
	t.Parallel()

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()

		auth, err := newGrpcResumeAuth(context.Background(), GrpcOAuthConfig{})
		require.NoError(t, err)
		require.IsType(t, noopGrpcResumeAuth{}, auth)
	})

	t.Run("partial config", func(t *testing.T) {
		t.Parallel()

		auth, err := newGrpcResumeAuth(context.Background(), GrpcOAuthConfig{ClientID: "client-id"})
		require.Error(t, err)
		require.Nil(t, auth)
	})

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()

		auth, err := newGrpcResumeAuth(context.Background(), GrpcOAuthConfig{
			ClientID:     " client-id ",
			ClientSecret: " secret ",
			TokenURL:     " https://tokens.example.com ",
		})
		require.NoError(t, err)
		require.IsType(t, oauthGrpcResumeAuth{}, auth)
	})
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
