package auth

import (
	"context"
	"net/http"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/token"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/token/jwks"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/token/oidc"
)

type ProviderConfig = token.ProviderConfig

type JWTConfig = jwks.Config

type JWTIssuer = jwks.Issuer

type AudienceMatchPolicy = jwks.AudienceMatchPolicy

const AudienceMatchAny = jwks.AudienceMatchAny

// IssuerVerifier verifies a JWT against the issuers in a ProviderConfig and
// returns its claims. It resolves no identity, so it suits any caller that
// needs to establish a token is validly signed without asking who it names.
type IssuerVerifier = token.AdminVerifier

// Deprecated: use IssuerVerifier. Admin service tokens were the first caller
// and the name followed them, which reads as a restriction that does not
// exist. Kept so callers can move in their own time.
type AdminVerifier = token.AdminVerifier

func ParseProviderConfig(value string) (ProviderConfig, error) {
	return token.ParseProviderConfig(value)
}

// NewIssuerVerifier builds a verifier for every issuer in the config,
// returning nil when it declares none. A nil verifier denies everything.
func NewIssuerVerifier(ctx context.Context, config ProviderConfig, httpClient *http.Client) (*IssuerVerifier, error) {
	return token.NewAdminVerifier(ctx, config, httpClient)
}

// Deprecated: use NewIssuerVerifier.
func NewAdminVerifier(ctx context.Context, config ProviderConfig, httpClient *http.Client) (*AdminVerifier, error) {
	return token.NewAdminVerifier(ctx, config, httpClient)
}

// IdentityLookup resolves an OIDC identity (issuer + subject) to an internal
// user UUID. Implementations should return ErrIdentityNotFound when no
// matching identity exists.
type IdentityLookup = oidc.IdentityLookup

// OIDCVerifier verifies auth-provider user JWTs for a single issuer and
// resolves the internal user identity through an IdentityLookup.
type OIDCVerifier = oidc.Verifier

// ErrIdentityNotFound is returned by OIDC verification when the token is
// valid but no matching identity exists.
var ErrIdentityNotFound = oidc.ErrIdentityNotFound

// NewOIDCVerifier constructs an OIDCVerifier for the given issuer config.
// External consumers (e.g. belt) use this to verify auth-provider tokens with
// their own identity storage.
func NewOIDCVerifier(ctx context.Context, config JWTConfig, httpClient *http.Client, identities IdentityLookup) (*OIDCVerifier, error) {
	return oidc.NewVerifier(ctx, config, httpClient, identities)
}

// ProviderVerifier verifies auth-provider user JWTs across every issuer in a
// ProviderConfig.
type ProviderVerifier = token.ProviderVerifier

// NewProviderVerifier constructs a ProviderVerifier for the given provider
// config. When the config has no JWT issuers it returns (nil, nil); a nil
// ProviderVerifier denies all verification attempts at runtime.
func NewProviderVerifier(ctx context.Context, config ProviderConfig, httpClient *http.Client, identities IdentityLookup) (*ProviderVerifier, error) {
	return token.NewProviderVerifier(ctx, config, httpClient, identities)
}
