package legacy

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// minSecretLength is the minimum length of a secret used to verify HMAC-signed
// JWTs. This is a security measure to prevent the use of weak secrets (like
// empty).
const minSecretLength = 16

// Verifier verifies HMAC-signed JWTs against a set of shared secrets.
type Verifier struct {
	secrets []string
	options []jwt.ParserOption
}

// NewVerifier constructs a Verifier from the supplied Config.
func NewVerifier(entry Config) (*Verifier, error) {
	options := []jwt.ParserOption{jwt.WithExpirationRequired()}

	var secrets []string
	if entry.HMAC != nil {
		secrets = make([]string, 0, len(entry.HMAC.Secrets))
		for _, secret := range entry.HMAC.Secrets {
			if len(secret) < minSecretLength {
				return nil, fmt.Errorf("jwt secret is too short, minimum length is %d", minSecretLength)
			}
			secrets = append(secrets, secret)
		}
	}

	return &Verifier{
		secrets: secrets,
		options: options,
	}, nil
}

// Verify parses and validates the supplied token string against each
// configured secret, returning the first successful verification.
func (v *Verifier) Verify(_ context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error) {
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
			errs = append(errs, fmt.Errorf("failed to verify auth provider legacy token: %w", err))

			continue
		}
		if !token.Valid {
			continue
		}

		claimValue, ok := claimString(claims, "sub")
		if !ok {
			errs = append(errs, errors.New("auth provider legacy token is missing sub claim"))

			continue
		}

		userID, parseErr := uuid.Parse(claimValue)
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("auth provider legacy token sub claim is not a valid uuid: %w", parseErr))

			continue
		}

		return userID, claims, nil
	}

	if len(errs) == 0 {
		return uuid.Nil, nil, errors.New("failed to verify auth provider legacy token, no usable secrets found")
	}

	return uuid.Nil, nil, errors.Join(errs...)
}

func claimString(claims jwt.MapClaims, name string) (string, bool) {
	value, ok := claims[name]
	if !ok {
		return "", false
	}

	switch typed := value.(type) {
	case string:
		return typed, typed != ""
	case []string:
		if len(typed) == 0 {
			return "", false
		}

		return typed[0], typed[0] != ""
	case []any:
		if len(typed) == 0 {
			return "", false
		}
		first, ok := typed[0].(string)

		return first, ok && first != ""
	default:
		return "", false
	}
}
