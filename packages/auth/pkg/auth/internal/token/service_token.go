package token

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/token/jwks"
)

// serviceTokenClockSkew is the leeway applied to time-based claims. Service
// tokens are short-lived, so a clock a little out of step would otherwise
// reject one that is legitimately current.
const serviceTokenClockSkew = 30 * time.Second

// ServiceTokenVerifier verifies JWTs against one or more configured issuers
// and returns the first successful verification.
//
// Keys come from each issuer's conventional JWKS path rather than an OIDC
// discovery document, which suits a token minted by a peer service: there is
// no discovery to perform, and no separate issuer declaration to cross-check.
type ServiceTokenVerifier struct {
	verifiers []*jwks.Verifier
}

// NewServiceTokenVerifier builds a verifier from the same ProviderConfig shape
// used for AUTH_PROVIDER_CONFIG. It returns nil when the config declares no
// issuers, leaving whichever scheme uses it unconfigured.
//
// Backs AdminJWTAuth today; nothing about it is specific to that scheme.
func NewServiceTokenVerifier(ctx context.Context, config ProviderConfig, httpClient *http.Client) (*ServiceTokenVerifier, error) {
	normalized := config.normalize()
	if !normalized.enabled() {
		return nil, nil
	}

	verifiers := make([]*jwks.Verifier, 0, len(normalized.JWT))
	for i, entry := range normalized.JWT {
		verifier, err := jwks.NewVerifierFromIssuerJWKS(ctx, entry, httpClient,
			jwks.WithParserOptions(jwt.WithLeeway(serviceTokenClockSkew)),
		)
		if err != nil {
			return nil, fmt.Errorf("service token jwt[%d]: %w", i, err)
		}
		verifiers = append(verifiers, verifier)
	}

	return &ServiceTokenVerifier{verifiers: verifiers}, nil
}

// Verify iterates over the configured issuers and returns the claims of the
// first successful verification.
func (v *ServiceTokenVerifier) Verify(ctx context.Context, tokenString string) (jwt.MapClaims, error) {
	if v == nil || len(v.verifiers) == 0 {
		return nil, errors.New("service token verifier is not configured")
	}

	errs := make([]error, 0, len(v.verifiers))
	for _, verifier := range v.verifiers {
		claims, err := verifier.Verify(ctx, tokenString)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		return claims, nil
	}

	return nil, fmt.Errorf("failed to verify service token: %w", errors.Join(errs...))
}
