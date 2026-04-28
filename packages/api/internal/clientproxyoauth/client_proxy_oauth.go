package clientproxyoauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Verifier interface {
	VerifyClaims(ctx context.Context, rawToken string) (Claims, error)
}

type Claims struct {
	Subject string
	OrgID   string
}

type TokenClaims struct {
	OrgID string `json:"org_id"`
}

type oidcVerifier struct {
	verifier *oidc.IDTokenVerifier
}

type noopVerifier struct{}

func NewVerifier(ctx context.Context, issuerURL string, audience string) (Verifier, error) {
	issuerURL = strings.TrimSpace(issuerURL)
	audience = strings.TrimSpace(audience)

	if issuerURL == "" && audience == "" {
		return noopVerifier{}, nil
	}
	if issuerURL == "" || audience == "" {
		return nil, errors.New("client proxy OIDC issuer URL and audience must both be configured")
	}

	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("create client proxy OIDC provider: %w", err)
	}

	return &oidcVerifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
	}, nil
}

func (noopVerifier) VerifyClaims(context.Context, string) (Claims, error) {
	return Claims{}, errors.New("client proxy OIDC verifier is not configured")
}

func (v *oidcVerifier) VerifyClaims(ctx context.Context, rawToken string) (Claims, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Claims{}, fmt.Errorf("verify client proxy OIDC token: %w", err)
	}

	claims := TokenClaims{}
	if err := idToken.Claims(&claims); err != nil {
		return Claims{}, fmt.Errorf("parse client proxy OIDC claims: %w", err)
	}

	return Claims{
		Subject: idToken.Subject,
		OrgID:   claims.OrgID,
	}, nil
}
