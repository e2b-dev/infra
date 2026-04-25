package auth

import (
	"context"
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

type authProviderJWTVerificationStrategy interface {
	verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error)
	close()
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
	switch {
	case jwtConfig.HMAC != nil:
		strategy = newHMACAuthProviderJWTVerifier(jwtConfig)
	case jwtConfig.JWKS != nil:
		jwksStrategy, err := newJWKSAuthProviderJWTVerifier(context.Background(), jwtConfig, *jwtConfig.JWKS)
		if err != nil {
			return nil, err
		}
		strategy = jwksStrategy
	default:
		return nil, errors.New("auth provider verifier has no configured signing verifier")
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

func (v *AuthProviderJWTVerifier) Close() {
	if v == nil || v.strategy == nil {
		return
	}

	v.strategy.close()
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
