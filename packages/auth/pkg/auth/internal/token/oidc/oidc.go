package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/token/jwks"
)

// ErrIdentityNotFound is returned by Verify when the token is valid but no
// matching row exists in public.user_identities for (iss, sub).
var ErrIdentityNotFound = errors.New("oidc identity not found")

// IdentityLookup resolves the internal user UUID for an OIDC identity
// (issuer + subject). Implementations should return ErrIdentityNotFound when
// no row matches the supplied pair.
type IdentityLookup interface {
	GetUserIdentity(ctx context.Context, iss, sub string) (uuid.UUID, error)
}

// Verifier verifies JWTs against a single OIDC issuer and resolves the
// internal user for the token's identity.
type Verifier struct {
	tokens     *jwks.Verifier
	identities IdentityLookup
}

// NewVerifier constructs a Verifier from the supplied Config. It performs the
// OIDC discovery fetch synchronously and fails fast on configuration or
// network errors.
func NewVerifier(ctx context.Context, entry jwks.Config, httpClient *http.Client, identities IdentityLookup) (*Verifier, error) {
	if identities == nil {
		return nil, errors.New("OIDC identity lookup is required")
	}

	tokens, err := jwks.NewVerifier(ctx, entry, httpClient)
	if err != nil {
		return nil, err
	}

	return &Verifier{
		tokens:     tokens,
		identities: identities,
	}, nil
}

// Verify parses and validates the supplied token string and resolves the
// internal user UUID for the (iss, sub) pair via the configured
// IdentityLookup. When the token is valid but no matching identity exists,
// the returned error wraps ErrIdentityNotFound.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error) {
	claims, err := v.tokens.Verify(ctx, tokenString)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to verify auth provider token: %w", err)
	}

	iss, ok := claimString(claims, "iss")
	if !ok {
		return uuid.Nil, nil, errors.New("auth provider token is missing iss claim")
	}

	sub, ok := claimString(claims, "sub")
	if !ok {
		return uuid.Nil, nil, errors.New("auth provider token is missing sub claim")
	}

	userID, err := v.identities.GetUserIdentity(ctx, iss, sub)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("resolve user identity for auth provider token: %w", err)
	}

	return userID, claims, nil
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
