package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// MinJWTSecretLength is the minimum length of a secret used to verify the Supabase JWT.
// This is a security measure to prevent the use of weak secrets (like empty).
const MinJWTSecretLength = 16

// SupabaseClaims defines the claims we expect from the Supabase JWT.
type SupabaseClaims struct {
	jwt.RegisteredClaims
}

// GetJWTClaims tries each secret to parse and validate the JWT token.
func GetJWTClaims(ctx context.Context, secrets []string, token string) (*SupabaseClaims, error) {
	errs := make([]error, 0)

	for _, secret := range secrets {
		if len(secret) < MinJWTSecretLength {
			logger.L().Warn(ctx, "jwt secret is too short and will be ignored",
				zap.Int("min_length", MinJWTSecretLength),
				zap.String("secret_start", secret[:min(3, len(secret))]))

			continue
		}

		parsed, err := jwt.ParseWithClaims(token, &SupabaseClaims{}, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}

			return []byte(secret), nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to parse supabase token: %w", err))

			continue
		}

		if claims, ok := parsed.Claims.(*SupabaseClaims); ok && parsed.Valid {
			return claims, nil
		}
	}

	if len(errs) == 0 {
		return nil, errors.New("failed to parse supabase token, no secrets found")
	}

	return nil, errors.Join(errs...)
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
