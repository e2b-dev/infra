package auth

import (
	"context"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// MinJWTSecretLength is the minimum length of a secret used to verify the Supabase JWT.
	// This is a security measure to prevent the use of weak secrets (like empty).
	MinJWTSecretLength = 16
)

// SupabaseClaims defines the claims we expect from the Supabase JWT.
type SupabaseClaims struct {
	jwt.RegisteredClaims
}

// GetJWTClaims tries each secret to parse and validate the JWT token.
func GetJWTClaims(ctx context.Context, secrets []string, token string) (*SupabaseClaims, error) {
	verifier, err := NewAuthProviderJWTVerifier(NewHMACAuthProviderConfig(secrets))
	if err != nil {
		return nil, fmt.Errorf("create supabase HMAC verifier: %w", err)
	}

	identity, err := verifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to parse supabase token: %w", err)
	}

	subject, ok := claimString(identity.Claims, "sub")
	if !ok {
		return nil, fmt.Errorf("failed getting jwt subject: missing subject")
	}

	return &SupabaseClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}}, nil
}

// ParseUserIDFromToken validates a Supabase JWT and extracts the user ID from the subject claim.
func ParseUserIDFromToken(ctx context.Context, secrets []string, supabaseToken string) (uuid.UUID, error) {
	claims, err := GetJWTClaims(ctx, secrets, supabaseToken)
	if err != nil {
		return uuid.UUID{}, err
	}

	userId, err := claims.GetSubject()
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("failed getting jwt subject: %w", err)
	}

	userIDParsed, err := uuid.Parse(userId)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("failed parsing user uuid: %w", err)
	}

	return userIDParsed, nil
}
