package gcpauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// NewTokenSource returns a caching, refreshing Application Default Credentials
// token source. On GCE this resolves to the service account attached to the VM.
func NewTokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	source, err := google.DefaultTokenSource(ctx, cloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("load Google Application Default Credentials: %w", err)
	}

	return oauth2.ReuseTokenSource(nil, source), nil
}

// NewRegistryAuthenticator returns an Artifact Registry authenticator backed
// by short-lived ADC access tokens. Authorization refreshes the token whenever
// its expiry window is reached; no registry password is retained.
func NewRegistryAuthenticator(ctx context.Context) (authn.Authenticator, error) {
	source, err := NewTokenSource(ctx)
	if err != nil {
		return nil, err
	}

	return NewRegistryAuthenticatorWithTokenSource(source), nil
}

// NewRegistryAuthenticatorWithTokenSource is the injectable form used by
// consumers and tests.
func NewRegistryAuthenticatorWithTokenSource(source oauth2.TokenSource) authn.Authenticator {
	if source == nil {
		return &registryAuthenticator{}
	}

	return &registryAuthenticator{source: oauth2.ReuseTokenSource(nil, source)}
}

type registryAuthenticator struct {
	source oauth2.TokenSource
}

func (a *registryAuthenticator) Authorization() (*authn.AuthConfig, error) {
	if a.source == nil {
		return nil, errors.New("Google ADC token source is nil")
	}

	token, err := a.source.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh Google ADC access token: %w", err)
	}
	if token.AccessToken == "" {
		return nil, errors.New("Google ADC returned an empty access token")
	}

	return &authn.AuthConfig{
		Username: "oauth2accesstoken",
		Password: token.AccessToken,
	}, nil
}
