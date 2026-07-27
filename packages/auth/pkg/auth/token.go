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

// ServiceTokenVerifier verifies a JWT whose keys are published at the
// issuer's conventional JWKS path, with no OIDC discovery document. Suited to
// tokens minted by a peer service, where there is no discovery to do.
//
// Not the right verifier for tokens from an identity provider: it neither
// fetches the discovery document nor cross-checks the issuer that document
// declares. Those callers want ProviderVerifier or IdentityVerifier.
type ServiceTokenVerifier = token.AdminVerifier

// Deprecated: use ServiceTokenVerifier. Admin service tokens were the first
// caller and the name followed them, which reads as a restriction that does
// not exist while hiding the one that does — no discovery.
type AdminVerifier = token.AdminVerifier

func ParseProviderConfig(value string) (ProviderConfig, error) {
	return token.ParseProviderConfig(value)
}

// NewServiceTokenVerifier builds a verifier for every issuer in the config,
// returning nil when it declares none. A nil verifier denies everything.
func NewServiceTokenVerifier(ctx context.Context, config ProviderConfig, httpClient *http.Client) (*ServiceTokenVerifier, error) {
	return token.NewAdminVerifier(ctx, config, httpClient)
}

// TokenIdentity is what a verified token asserts about itself: the issuer and
// subject it names, and the claims it carried.
type TokenIdentity = oidc.TokenIdentity

// IdentityVerifier verifies auth-provider tokens and reports what they assert
// without resolving anyone. The same type as ProviderVerifier, constructed
// without a lookup, so only VerifyIdentity is available on it.
type IdentityVerifier = token.ProviderVerifier

// NewIdentityVerifier constructs a verifier for a caller that must read a
// token's subject before that subject is a user here — signup, or anything
// else that creates the identity it is authenticating.
//
// Performs the same discovery and issuer validation as NewProviderVerifier,
// so declining to resolve weakens nothing about the token itself. The
// alternative — handing the resolving verifier a lookup that answers
// "nobody" — makes not-found a value and turns any missed check into an
// authenticated nobody. This keeps that answer unrepresentable.
func NewIdentityVerifier(ctx context.Context, config ProviderConfig, httpClient *http.Client) (*IdentityVerifier, error) {
	return token.NewIdentityVerifier(ctx, config, httpClient)
}

// Deprecated: use NewServiceTokenVerifier.
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
