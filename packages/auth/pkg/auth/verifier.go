package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/bearer"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
)

// ProviderConfig describes external auth provider verification.
//
// `jwt` entries are OIDC-compliant issuers (asymmetric keys discovered via
// the OIDC discovery document). `bearer` entries are non-OIDC HMAC-signed
// JWTs.
type ProviderConfig struct {
	JWT    []oidc.Entry   `json:"jwt"`
	Bearer []bearer.Entry `json:"bearer"`
}

// Enabled returns true when at least one auth provider entry is configured.
func (c ProviderConfig) Enabled() bool {
	return len(c.JWT) > 0 || len(c.Bearer) > 0
}

// normalize applies defaults across both arrays and returns a copy.
func (c ProviderConfig) normalize() ProviderConfig {
	jwts := make([]oidc.Entry, len(c.JWT))
	for i, entry := range c.JWT {
		jwts[i] = entry.Normalized()
	}

	bearers := make([]bearer.Entry, len(c.Bearer))
	for i, entry := range c.Bearer {
		bearers[i] = entry.Normalized()
	}

	return ProviderConfig{JWT: jwts, Bearer: bearers}
}

// validate runs configuration sanity checks on a (already normalized) config.
func (c ProviderConfig) validate() error {
	for i, entry := range c.JWT {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("auth provider jwt[%d]: %w", i, err)
		}
	}

	for i, entry := range c.Bearer {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("auth provider bearer[%d]: %w", i, err)
		}
	}

	return nil
}

// Identity is the normalized identity extracted from a validated auth
// provider JWT. It is an alias of jwtutil.Identity, which holds the canonical
// type definition.
type Identity = jwtutil.Identity

// Strategy is the interface satisfied by per-provider JWT verifiers used by
// Verifier.
type Strategy interface {
	Verify(ctx context.Context, tokenString string) (*jwtutil.Identity, error)
}

type Verifier struct {
	strategies []Strategy
}

func NewVerifier(ctx context.Context, config ProviderConfig) (*Verifier, error) {
	return newVerifier(ctx, config, &http.Client{Timeout: oidc.JWKSHTTPTimeout})
}

func newVerifier(ctx context.Context, config ProviderConfig, jwksHTTPClient *http.Client) (*Verifier, error) {
	normalized := config.normalize()
	if err := normalized.validate(); err != nil {
		return nil, err
	}
	if !normalized.Enabled() {
		return nil, nil
	}

	strategies := make([]Strategy, 0, len(normalized.JWT)+len(normalized.Bearer))

	for i, entry := range normalized.JWT {
		s, err := oidc.NewVerifier(ctx, entry, jwksHTTPClient)
		if err != nil {
			return nil, fmt.Errorf("auth provider jwt[%d]: %w", i, err)
		}
		strategies = append(strategies, s)
	}

	for i, entry := range normalized.Bearer {
		s, err := bearer.NewVerifier(entry)
		if err != nil {
			return nil, fmt.Errorf("auth provider bearer[%d]: %w", i, err)
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

func (v *Verifier) Verify(ctx context.Context, tokenString string) (*Identity, error) {
	if v == nil {
		return nil, errors.New("auth provider verifier is not configured")
	}

	if len(v.strategies) == 0 {
		return nil, errors.New("auth provider verifier strategies are not configured")
	}

	errs := make([]error, 0, len(v.strategies))
	for _, strategy := range v.strategies {
		identity, err := strategy.Verify(ctx, tokenString)
		if err == nil {
			return identity, nil
		}

		errs = append(errs, err)
	}

	return nil, fmt.Errorf("failed to verify auth provider token: %w", errors.Join(errs...))
}
