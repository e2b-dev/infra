package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/legacy"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
)

// ProviderConfig describes external auth provider verification.
//
// `jwt` entries are OIDC-compliant issuers (asymmetric keys discovered via
// the OIDC discovery document). `legacy` is an optional non-OIDC HMAC-signed
// JWT source.
type ProviderConfig struct {
	JWT    []oidc.Config  `json:"jwt"`
	Legacy *legacy.Config `json:"legacy"`
}

// enabled returns true when at least one auth provider entry is configured.
func (c ProviderConfig) enabled() bool {
	return len(c.JWT) > 0 || c.Legacy != nil
}

// normalize applies defaults across both arrays and returns a copy.
func (c ProviderConfig) normalize() ProviderConfig {
	jwts := make([]oidc.Config, len(c.JWT))
	for i, entry := range c.JWT {
		jwts[i] = entry.Normalized()
	}

	var legacyEntry *legacy.Config
	if c.Legacy != nil {
		normalized := c.Legacy.Normalized()
		legacyEntry = &normalized
	}

	return ProviderConfig{JWT: jwts, Legacy: legacyEntry}
}

// validate runs configuration sanity checks on a (already normalized) config.
func (c ProviderConfig) validate() error {
	for i, entry := range c.JWT {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("auth provider jwt[%d]: %w", i, err)
		}
	}

	if c.Legacy != nil {
		if err := c.Legacy.Validate(); err != nil {
			return fmt.Errorf("auth provider legacy: %w", err)
		}
	}

	return nil
}

// strategy is the interface satisfied by per-provider JWT verifiers used by
// Verifier.
type strategy interface {
	Verify(ctx context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error)
}

// Verifier aggregates one or more JWT verification strategies (OIDC issuers
// plus an optional legacy HMAC source) and returns the first that succeeds.
type Verifier struct {
	strategies []strategy
}

// NewVerifier constructs a *Verifier from the given ProviderConfig.
//
// When the provided config has no JWT issuers and no legacy entry (i.e. the
// AUTH_PROVIDER_CONFIG env var is unset or empty), NewVerifier returns
// (nil, nil). This is a valid configuration: the caller can pass the nil
// Verifier to authService, and any token verification attempt will be denied
// at runtime by Verifier.Verify / Service.ValidateAuthProviderToken.
func NewVerifier(ctx context.Context, config ProviderConfig, oidcHTTPClient *http.Client, identities oidc.IdentityLookup) (*Verifier, error) {
	normalized := config.normalize()
	if err := normalized.validate(); err != nil {
		return nil, err
	}
	if !normalized.enabled() {
		return nil, nil
	}

	strategiesCap := len(normalized.JWT)
	if normalized.Legacy != nil {
		strategiesCap++
	}
	strategies := make([]strategy, 0, strategiesCap)

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

	if normalized.Legacy != nil {
		s, err := legacy.NewVerifier(*normalized.Legacy)
		if err != nil {
			return nil, fmt.Errorf("auth provider legacy: %w", err)
		}
		strategies = append(strategies, s)
	}

	if len(strategies) == 0 {
		return nil, errors.New("auth provider verifier has no configured signing verifier")
	}

	return &Verifier{
		strategies: strategies,
	}, nil
}

// Verify iterates over the configured strategies and returns the first that
// successfully verifies the token and resolves a non-nil internal user UUID.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error) {
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
