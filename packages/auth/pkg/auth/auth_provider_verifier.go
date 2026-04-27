package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

type authProviderJWTVerificationStrategy interface {
	verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error)
	close()
}

type AuthProviderJWTVerifier struct {
	strategies []authProviderJWTVerificationStrategy
}

func NewAuthProviderJWTVerifier(ctx context.Context, config AuthProviderConfig) (*AuthProviderJWTVerifier, error) {
	jwtConfig := config.normalizedJWT()
	if err := jwtConfig.validate(); err != nil {
		return nil, err
	}
	if !jwtConfig.Enabled() {
		return nil, nil
	}

	strategies := make([]authProviderJWTVerificationStrategy, 0, 2)
	if jwtConfig.HMAC != nil {
		strategies = append(strategies, newHMACAuthProviderJWTVerifier(jwtConfig))
	}
	if jwtConfig.JWKS != nil {
		jwksStrategy, err := newJWKSAuthProviderJWTVerifier(ctx, jwtConfig, *jwtConfig.JWKS)
		if err != nil {
			return nil, err
		}
		strategies = append(strategies, jwksStrategy)
	}
	if len(strategies) == 0 {
		return nil, errors.New("auth provider verifier has no configured signing verifier")
	}

	return &AuthProviderJWTVerifier{
		strategies: strategies,
	}, nil
}

func (v *AuthProviderJWTVerifier) Verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
	if v == nil {
		return nil, errors.New("auth provider verifier is not configured")
	}

	if len(v.strategies) == 0 {
		return nil, errors.New("auth provider verifier strategies are not configured")
	}

	errs := make([]error, 0, len(v.strategies))
	for _, strategy := range v.strategies {
		identity, err := strategy.verify(ctx, tokenString)
		if err == nil {
			return identity, nil
		}

		errs = append(errs, err)
	}

	return nil, fmt.Errorf("failed to verify auth provider token: %w", errors.Join(errs...))
}

func (v *AuthProviderJWTVerifier) Close() {
	if v == nil {
		return
	}

	for _, strategy := range v.strategies {
		strategy.close()
	}
}

func authProviderJWTParserOptions(issuer, audience string) []jwt.ParserOption {
	options := []jwt.ParserOption{jwt.WithExpirationRequired()}
	if issuer != "" {
		options = append(options, jwt.WithIssuer(issuer))
	}
	if audience != "" {
		options = append(options, jwt.WithAudience(audience))
	}

	return options
}
