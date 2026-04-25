package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type hmacAuthProviderJWTVerifier struct {
	config AuthProviderJWTConfig
}

func newHMACAuthProviderJWTVerifier(config AuthProviderJWTConfig) *hmacAuthProviderJWTVerifier {
	return &hmacAuthProviderJWTVerifier{config: config}
}

func (v *hmacAuthProviderJWTVerifier) verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
	errs := make([]error, 0, len(v.config.HMACSecrets))
	for _, secret := range v.config.HMACSecrets {
		if len(secret) < MinJWTSecretLength {
			logger.L().Warn(ctx, "jwt secret is too short and will be ignored",
				zap.Int("min_length", MinJWTSecretLength),
				zap.String("secret_start", secret[:min(3, len(secret))]))

			continue
		}

		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected auth provider signing method: %v", token.Header["alg"])
			}

			return []byte(secret), nil
		}, authProviderJWTParserOptions(v.config)...)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to verify auth provider HMAC token: %w", err))

			continue
		}
		if token.Valid {
			return identityFromClaims(claims, v.config.UserIDClaim, v.config.EmailClaim), nil
		}
	}

	if len(errs) == 0 {
		return nil, errors.New("failed to verify auth provider HMAC token, no usable secrets found")
	}

	return nil, errors.Join(errs...)
}
