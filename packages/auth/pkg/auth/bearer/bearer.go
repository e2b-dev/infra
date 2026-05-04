package bearer

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
)

// MinSecretLength is the minimum length of a secret used to verify HMAC-signed
// JWTs. This is a security measure to prevent the use of weak secrets (like
// empty).
const MinSecretLength = 16

// Verifier verifies HMAC-signed JWTs against a set of shared secrets.
type Verifier struct {
	secrets     []string
	userIDClaim string
	audiences   []string
	options     []jwt.ParserOption
}

// NewVerifier constructs a Verifier from the supplied Entry.
func NewVerifier(entry Entry) (*Verifier, error) {
	options := []jwt.ParserOption{jwt.WithExpirationRequired()}

	var secrets []string
	if entry.HMAC != nil {
		secrets = make([]string, 0, len(entry.HMAC.Secrets))
		for _, secret := range entry.HMAC.Secrets {
			if len(secret) < MinSecretLength {
				return nil, fmt.Errorf("jwt secret is too short, minimum length is %d", MinSecretLength)
			}
			secrets = append(secrets, secret)
		}
	}

	return &Verifier{
		secrets:     secrets,
		userIDClaim: entry.ClaimMappings.Username.Claim,
		audiences:   entry.Audiences,
		options:     options,
	}, nil
}

// Verify parses and validates the supplied token string against each
// configured secret, returning the first successful verification.
func (v *Verifier) Verify(_ context.Context, tokenString string) (*jwtutil.Identity, error) {
	errs := make([]error, 0, len(v.secrets))
	for _, secret := range v.secrets {
		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected auth provider signing method: %v", token.Header["alg"])
			}

			return []byte(secret), nil
		}, v.options...)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to verify auth provider bearer token: %w", err))

			continue
		}
		if !token.Valid {
			continue
		}

		if err := jwtutil.ValidateAudience(claims, v.audiences); err != nil {
			errs = append(errs, fmt.Errorf("failed to verify auth provider bearer token: %w", err))

			continue
		}

		return jwtutil.IdentityFromClaims(claims, v.userIDClaim), nil
	}

	if len(errs) == 0 {
		return nil, errors.New("failed to verify auth provider bearer token, no usable secrets found")
	}

	return nil, errors.Join(errs...)
}
