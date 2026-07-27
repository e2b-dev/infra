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

// TokenIdentity is what a verified token asserts about itself, before
// anything has been looked up. Both claims are required: a token that names
// no issuer or no subject identifies nobody, whatever its signature says.
type TokenIdentity struct {
	Issuer  string
	Subject string
	Claims  jwt.MapClaims
}

// Verifier verifies JWTs against a single OIDC issuer.
//
// It answers at two levels. VerifyIdentity establishes what the token says it
// is; Verify goes further and resolves the internal user behind it. The
// second needs an IdentityLookup, the first does not — which matters for a
// caller acting before the user it will name exists.
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

	return newVerifier(ctx, entry, httpClient, identities)
}

// NewIdentityVerifier constructs a Verifier that establishes what a token
// asserts without resolving it to a user. Only VerifyIdentity is available;
// Verify reports that no lookup is configured.
//
// Same discovery and issuer validation as NewVerifier — the difference is
// what the result is used for, not how far the token is trusted.
func NewIdentityVerifier(ctx context.Context, entry jwks.Config, httpClient *http.Client) (*Verifier, error) {
	return newVerifier(ctx, entry, httpClient, nil)
}

func newVerifier(ctx context.Context, entry jwks.Config, httpClient *http.Client, identities IdentityLookup) (*Verifier, error) {
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
	if v.identities == nil {
		return uuid.Nil, nil, errors.New("auth provider verifier has no identity lookup")
	}

	identity, err := v.VerifyIdentity(ctx, tokenString)
	if err != nil {
		return uuid.Nil, nil, err
	}

	userID, err := v.identities.GetUserIdentity(ctx, identity.Issuer, identity.Subject)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("resolve user identity for auth provider token: %w", err)
	}

	return userID, identity.Claims, nil
}

// VerifyIdentity validates the token and reports the issuer and subject it
// asserts, without consulting any identity store.
func (v *Verifier) VerifyIdentity(ctx context.Context, tokenString string) (TokenIdentity, error) {
	claims, err := v.tokens.Verify(ctx, tokenString)
	if err != nil {
		return TokenIdentity{}, fmt.Errorf("failed to verify auth provider token: %w", err)
	}

	iss, ok := claimString(claims, "iss")
	if !ok {
		return TokenIdentity{}, errors.New("auth provider token is missing iss claim")
	}

	sub, ok := claimString(claims, "sub")
	if !ok {
		return TokenIdentity{}, errors.New("auth provider token is missing sub claim")
	}

	return TokenIdentity{Issuer: iss, Subject: sub, Claims: claims}, nil
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
