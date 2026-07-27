package token

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/token/jwks"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/token/oidc"
)

// ProviderConfig describes external auth provider verification.
type ProviderConfig struct {
	JWT []jwks.Config `json:"jwt"`
}

// enabled returns true when at least one auth provider entry is configured.
func (c ProviderConfig) enabled() bool {
	return len(c.JWT) > 0
}

// normalize applies defaults across both arrays and returns a copy.
func (c ProviderConfig) normalize() ProviderConfig {
	jwts := make([]jwks.Config, len(c.JWT))
	for i, entry := range c.JWT {
		jwts[i] = entry.Normalized()
	}

	return ProviderConfig{JWT: jwts}
}

// validate runs configuration sanity checks on a (already normalized) config.
func (c ProviderConfig) validate() error {
	for i, entry := range c.JWT {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("auth provider jwt[%d]: %w", i, err)
		}
	}

	return nil
}

// strategy is the interface satisfied by per-issuer JWT verifiers.
type strategy interface {
	Verify(ctx context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error)
	VerifyIdentity(ctx context.Context, tokenString string) (oidc.TokenIdentity, error)
}

// OIDCVerifier verifies auth-provider JWTs against every issuer in a
// ProviderConfig, via each issuer's OIDC discovery document, and reports what
// the first accepting issuer says the token asserts.
//
// It links the token to nothing. A caller that needs the internal user behind
// it wants LinkedOIDCVerifier, which is this plus a lookup.
type OIDCVerifier struct {
	strategies []strategy
}

// LinkedOIDCVerifier is an OIDCVerifier that also resolves the asserted
// identity to an internal user.
//
// Separate type rather than a flag, so a verifier built without a lookup has
// no Verify to call. The capability is visible where the verifier is passed
// rather than discovered when a request reaches it.
type LinkedOIDCVerifier struct {
	OIDCVerifier

	identities oidc.IdentityLookup
}

// NewOIDCVerifier builds a verifier over every issuer in the config,
// returning (nil, nil) when it declares none. A nil verifier denies
// everything, so an unconfigured provider needs no branch at the call site.
func NewOIDCVerifier(ctx context.Context, config ProviderConfig, oidcHTTPClient *http.Client) (*OIDCVerifier, error) {
	return newOIDCVerifier(ctx, config, oidcHTTPClient, nil)
}

// NewLinkedOIDCVerifier builds a verifier that also resolves the asserted
// identity through the supplied lookup.
func NewLinkedOIDCVerifier(ctx context.Context, config ProviderConfig, oidcHTTPClient *http.Client, identities oidc.IdentityLookup) (*LinkedOIDCVerifier, error) {
	// Only when there is something to resolve. A config declaring no issuers
	// yields a nil verifier that denies everything, and demanding a lookup to
	// reach that conclusion would make an unconfigured provider a startup
	// failure rather than a supported state.
	if len(config.normalize().JWT) > 0 && identities == nil {
		return nil, errors.New("auth provider OIDC identity lookup is required when JWT issuers are configured")
	}

	verifier, err := newOIDCVerifier(ctx, config, oidcHTTPClient, identities)
	if err != nil || verifier == nil {
		return nil, err
	}

	return &LinkedOIDCVerifier{OIDCVerifier: *verifier, identities: identities}, nil
}

func newOIDCVerifier(ctx context.Context, config ProviderConfig, oidcHTTPClient *http.Client, identities oidc.IdentityLookup) (*OIDCVerifier, error) {
	normalized := config.normalize()
	if err := normalized.validate(); err != nil {
		return nil, err
	}
	if !normalized.enabled() {
		return nil, nil
	}

	strategies := make([]strategy, 0, len(normalized.JWT))
	for i, entry := range normalized.JWT {
		s, err := newStrategy(ctx, entry, oidcHTTPClient, identities)
		if err != nil {
			return nil, fmt.Errorf("auth provider jwt[%d]: %w", i, err)
		}
		strategies = append(strategies, s)
	}

	if len(strategies) == 0 {
		return nil, errors.New("auth provider verifier has no configured signing verifier")
	}

	return &OIDCVerifier{strategies: strategies}, nil
}

// newStrategy builds the per-issuer verifier. Both levels perform the same
// discovery and issuer validation; the lookup only decides whether the result
// can be resolved further.
func newStrategy(ctx context.Context, entry jwks.Config, httpClient *http.Client, identities oidc.IdentityLookup) (strategy, error) {
	if identities == nil {
		return oidc.NewIdentityVerifier(ctx, entry, httpClient)
	}

	return oidc.NewVerifier(ctx, entry, httpClient, identities)
}

// VerifyIdentity iterates over the configured issuers and returns what the
// first one to accept the token says it asserts.
//
// Unlike Verify there is no non-nil user id to insist on, because nothing has
// been resolved: the caller is asking who the token claims to be, which is
// the question worth asking before that person exists.
func (v *OIDCVerifier) VerifyIdentity(ctx context.Context, tokenString string) (oidc.TokenIdentity, error) {
	if v == nil {
		return oidc.TokenIdentity{}, errors.New("auth provider verifier is not configured")
	}

	if len(v.strategies) == 0 {
		return oidc.TokenIdentity{}, errors.New("auth provider verifier strategies are not configured")
	}

	errs := make([]error, 0, len(v.strategies))
	for _, strategy := range v.strategies {
		identity, err := strategy.VerifyIdentity(ctx, tokenString)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		return identity, nil
	}

	return oidc.TokenIdentity{}, fmt.Errorf("failed to verify auth provider token: %w", errors.Join(errs...))
}

// Verify iterates over the configured strategies and returns the first that
// successfully verifies the token and resolves a non-nil internal user UUID.
// VerifyIdentity shadows the promoted method so a nil verifier denies rather
// than panics. Promotion computes the address of the embedded value, which
// dereferences the outer pointer before the inner nil check can run, and a
// nil verifier is a supported state here: an unconfigured provider yields one
// and callers pass it along.
func (v *LinkedOIDCVerifier) VerifyIdentity(ctx context.Context, tokenString string) (oidc.TokenIdentity, error) {
	if v == nil {
		return oidc.TokenIdentity{}, errors.New("auth provider verifier is not configured")
	}

	return v.OIDCVerifier.VerifyIdentity(ctx, tokenString)
}

func (v *LinkedOIDCVerifier) Verify(ctx context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error) {
	if v == nil {
		return uuid.Nil, nil, errors.New("auth provider verifier is not configured")
	}

	if len(v.strategies) == 0 {
		return uuid.Nil, nil, errors.New("auth provider verifier strategies are not configured")
	}

	errs := make([]error, 0, len(v.strategies))
	for _, strategy := range v.strategies {
		userID, claims, err := strategy.Verify(ctx, tokenString)
		if err != nil {
			errs = append(errs, err)

			continue
		}
		if userID == uuid.Nil {
			errs = append(errs, errors.New("auth provider verifier strategy returned no user id"))

			continue
		}

		return userID, claims, nil
	}

	return uuid.Nil, nil, fmt.Errorf("failed to verify auth provider token: %w", errors.Join(errs...))
}
