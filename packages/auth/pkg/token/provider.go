package token

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/token/jwks"
	"github.com/e2b-dev/infra/packages/auth/pkg/token/oidc"
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

// strategy is the interface satisfied by per-provider JWT verifiers used by
// ProviderVerifier.
type strategy interface {
	Verify(ctx context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error)
}

// ProviderVerifier aggregates one or more OIDC JWT verification strategies and
// returns the first that succeeds.
type ProviderVerifier struct {
	strategies []strategy
}

// NewProviderVerifier constructs a *ProviderVerifier from the given
// ProviderConfig.
//
// When the provided config has no JWT issuers, NewProviderVerifier returns
// (nil, nil). This is a valid configuration: the caller can pass the nil
// ProviderVerifier along, and any token verification attempt will be denied at
// runtime by ProviderVerifier.Verify.
func NewProviderVerifier(ctx context.Context, config ProviderConfig, oidcHTTPClient *http.Client, identities oidc.IdentityLookup) (*ProviderVerifier, error) {
	normalized := config.normalize()
	if err := normalized.validate(); err != nil {
		return nil, err
	}
	if !normalized.enabled() {
		return nil, nil
	}

	strategies := make([]strategy, 0, len(normalized.JWT))

	if len(normalized.JWT) > 0 && identities == nil {
		return nil, errors.New("auth provider OIDC identity lookup is required when JWT issuers are configured")
	}

	for i, entry := range normalized.JWT {
		s, err := oidc.NewVerifier(ctx, entry, oidcHTTPClient, identities)
		if err != nil {
			return nil, fmt.Errorf("auth provider jwt[%d]: %w", i, err)
		}
		strategies = append(strategies, s)
	}

	if len(strategies) == 0 {
		return nil, errors.New("auth provider verifier has no configured signing verifier")
	}

	return &ProviderVerifier{
		strategies: strategies,
	}, nil
}

// Verify iterates over the configured strategies and returns the first that
// successfully verifies the token and resolves a non-nil internal user UUID.
func (v *ProviderVerifier) Verify(ctx context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error) {
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
