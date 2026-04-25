package auth

import (
	"context"
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

type authProviderJWTVerificationStrategy interface {
	verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error)
}

type AuthProviderJWTVerifier struct {
	strategy authProviderJWTVerificationStrategy
}

func NewAuthProviderJWTVerifier(config AuthProviderConfig) (*AuthProviderJWTVerifier, error) {
	jwtConfig := config.normalizedJWT()
	if err := jwtConfig.validate(); err != nil {
		return nil, err
	}
	if !jwtConfig.Enabled() {
		return nil, nil
	}

	var strategy authProviderJWTVerificationStrategy
	switch jwtConfig.SigningMethod {
	case authProviderSigningMethodHMAC:
		strategy = newHMACAuthProviderJWTVerifier(jwtConfig)
	case authProviderSigningMethodJWKS:
		strategy = newJWKSAuthProviderJWTVerifier(jwtConfig)
	default:
		return nil, errors.New("auth provider verifier has unknown signing method")
	}

	return &AuthProviderJWTVerifier{
		strategy: strategy,
	}, nil
}

func (v *AuthProviderJWTVerifier) Verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
	if v == nil {
		return nil, errors.New("auth provider verifier is not configured")
	}

	if v.strategy == nil {
		return nil, errors.New("auth provider verifier strategy is not configured")
	}

	return v.strategy.verify(ctx, tokenString)
}

func authProviderJWTParserOptions(config AuthProviderJWTConfig) []jwt.ParserOption {
	options := []jwt.ParserOption{jwt.WithExpirationRequired()}
	if config.Issuer != "" {
		options = append(options, jwt.WithIssuer(config.Issuer))
	}
	if config.Audience != "" {
		options = append(options, jwt.WithAudience(config.Audience))
	}

	return options
}
