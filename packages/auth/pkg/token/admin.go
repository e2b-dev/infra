package token

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/e2b-dev/infra/packages/auth/pkg/token/jwks"
)

// adminJWTClockSkew is the leeway applied to time-based claims of admin
// service JWTs.
const adminJWTClockSkew = 30 * time.Second

// AdminVerifier verifies admin service JWTs against one or more configured
// issuers and returns the first successful verification.
type AdminVerifier struct {
	verifiers []*jwks.Verifier
}

// NewAdminVerifier builds the verifier for the AdminJWTAuth security
// scheme from the same ProviderConfig shape used for AUTH_PROVIDER_CONFIG:
// short-lived service tokens whose signing methods are declared by JWKS keys.
// It returns nil when the config has no issuers, leaving the scheme unconfigured.
func NewAdminVerifier(ctx context.Context, config ProviderConfig, httpClient *http.Client) (*AdminVerifier, error) {
	normalized := config.normalize()
	if !normalized.enabled() {
		return nil, nil
	}

	verifiers := make([]*jwks.Verifier, 0, len(normalized.JWT))
	for i, entry := range normalized.JWT {
		verifier, err := jwks.NewVerifierFromIssuerJWKS(ctx, entry, httpClient,
			jwks.WithParserOptions(jwt.WithLeeway(adminJWTClockSkew)),
		)
		if err != nil {
			return nil, fmt.Errorf("admin JWT jwt[%d]: %w", i, err)
		}
		verifiers = append(verifiers, verifier)
	}

	return &AdminVerifier{verifiers: verifiers}, nil
}

// Verify iterates over the configured issuers and returns the claims of the
// first successful verification.
func (v *AdminVerifier) Verify(ctx context.Context, tokenString string) (jwt.MapClaims, error) {
	if v == nil || len(v.verifiers) == 0 {
		return nil, errors.New("admin JWT verifier is not configured")
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

	return nil, fmt.Errorf("failed to verify admin JWT: %w", errors.Join(errs...))
}
