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
	secrets     []string
	userIDClaim string
	options     []jwt.ParserOption
}

func newHMACAuthProviderJWTVerifier(config AuthProviderJWTConfig) *hmacAuthProviderJWTVerifier {
	return &hmacAuthProviderJWTVerifier{
		secrets:     config.HMAC.Secrets,
		userIDClaim: config.UserIDClaim,
		options:     authProviderJWTParserOptions(config.Issuer, config.Audience),
	}
}

func (v *hmacAuthProviderJWTVerifier) verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
	errs := make([]error, 0, len(v.secrets))
	for _, secret := range v.secrets {
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
		}, v.options...)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to verify auth provider HMAC token: %w", err))

			continue
		}
		if token.Valid {
			return identityFromClaims(claims, v.userIDClaim), nil
		}
	}

	if len(errs) == 0 {
		return nil, errors.New("failed to verify auth provider HMAC token, no usable secrets found")
	}

	return nil, errors.Join(errs...)
}

func (v *hmacAuthProviderJWTVerifier) close() {}
