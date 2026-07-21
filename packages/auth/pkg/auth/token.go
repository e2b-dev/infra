package auth

import (
	"context"
	"net/http"

	"github.com/e2b-dev/infra/packages/auth/internal/token"
	"github.com/e2b-dev/infra/packages/auth/internal/token/jwks"
)

type ProviderConfig = token.ProviderConfig

type JWTConfig = jwks.Config

type JWTIssuer = jwks.Issuer

type AudienceMatchPolicy = jwks.AudienceMatchPolicy

const AudienceMatchAny = jwks.AudienceMatchAny

type AdminVerifier = token.AdminVerifier

func ParseProviderConfig(value string) (ProviderConfig, error) {
	return token.ParseProviderConfig(value)
}

func NewAdminVerifier(ctx context.Context, config ProviderConfig, httpClient *http.Client) (*AdminVerifier, error) {
	return token.NewAdminVerifier(ctx, config, httpClient)
}
