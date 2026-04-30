package proxy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc/metadata"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

const grpcResumeAuthScope = proxygrpc.ScopeSandboxLifecycle

type grpcResumeAuth interface {
	authorize(ctx context.Context) (context.Context, error)
}

type noopGrpcResumeAuth struct{}

type oauthGrpcResumeAuth struct {
	tokenSource oauth2.TokenSource
}

func (c GRPCOAuthConfig) Enabled() bool {
	return strings.TrimSpace(c.ClientID) != "" ||
		strings.TrimSpace(c.ClientSecret) != "" ||
		strings.TrimSpace(c.TokenURL) != ""
}

func newGrpcResumeAuth(ctx context.Context, c GRPCOAuthConfig) (grpcResumeAuth, error) {
	if !c.Enabled() {
		return noopGrpcResumeAuth{}, nil
	}

	if strings.TrimSpace(c.ClientID) == "" ||
		strings.TrimSpace(c.ClientSecret) == "" ||
		strings.TrimSpace(c.TokenURL) == "" {
		return nil, errors.New("api grpc OAuth client ID, client secret, and token URL are required when OAuth is configured")
	}

	oauthConfig := clientcredentials.Config{
		ClientID:     strings.TrimSpace(c.ClientID),
		ClientSecret: strings.TrimSpace(c.ClientSecret),
		TokenURL:     strings.TrimSpace(c.TokenURL),
		Scopes:       []string{grpcResumeAuthScope},
	}

	return oauthGrpcResumeAuth{tokenSource: oauthConfig.TokenSource(ctx)}, nil
}

func (noopGrpcResumeAuth) authorize(ctx context.Context) (context.Context, error) {
	return ctx, nil
}

func (a oauthGrpcResumeAuth) authorize(ctx context.Context) (context.Context, error) {
	token, err := a.tokenSource.Token()
	if err != nil {
		return ctx, fmt.Errorf("get api grpc OAuth token: %w", err)
	}

	return metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataAuthorization, "Bearer "+token.AccessToken), nil
}
