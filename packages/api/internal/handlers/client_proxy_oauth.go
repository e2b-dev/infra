package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

type ClientProxyOAuthVerifier interface {
	VerifyClaims(ctx context.Context, rawToken string) (ClientProxyOAuthClaims, error)
}

type ClientProxyOAuthClaims struct {
	Subject string
	OrgID   string
}

type oidcClientProxyOAuthVerifier struct {
	verifier *oidc.IDTokenVerifier
}

type noopClientProxyOAuthVerifier struct{}

func NewClientProxyOAuthVerifier(ctx context.Context, issuerURL string, audience string) (ClientProxyOAuthVerifier, error) {
	issuerURL = strings.TrimSpace(issuerURL)
	audience = strings.TrimSpace(audience)

	if issuerURL == "" && audience == "" {
		return noopClientProxyOAuthVerifier{}, nil
	}
	if issuerURL == "" || audience == "" {
		return nil, errors.New("client proxy OIDC issuer URL and audience must both be configured")
	}

	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("create client proxy OIDC provider: %w", err)
	}

	return &oidcClientProxyOAuthVerifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
	}, nil
}

func (noopClientProxyOAuthVerifier) VerifyClaims(context.Context, string) (ClientProxyOAuthClaims, error) {
	return ClientProxyOAuthClaims{}, errors.New("client proxy OIDC verifier is not configured")
}

func (v *oidcClientProxyOAuthVerifier) VerifyClaims(ctx context.Context, rawToken string) (ClientProxyOAuthClaims, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return ClientProxyOAuthClaims{}, fmt.Errorf("verify client proxy OIDC token: %w", err)
	}

	claims := struct {
		OrgID string `json:"org_id"`
	}{}
	if err := idToken.Claims(&claims); err != nil {
		return ClientProxyOAuthClaims{}, fmt.Errorf("parse client proxy OIDC claims: %w", err)
	}

	return ClientProxyOAuthClaims{
		Subject: idToken.Subject,
		OrgID:   claims.OrgID,
	}, nil
}
