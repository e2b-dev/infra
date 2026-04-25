package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AuthProviderJWTVerifier struct {
	config AuthProviderJWTConfig
	client *http.Client

	mu        sync.RWMutex
	keys      map[string]any
	expiresAt time.Time
}

func NewAuthProviderJWTVerifier(config AuthProviderConfig) (*AuthProviderJWTVerifier, error) {
	jwtConfig := config.normalizedJWT()
	if err := jwtConfig.validate(); err != nil {
		return nil, err
	}
	if !jwtConfig.Enabled() {
		return nil, nil
	}

	return &AuthProviderJWTVerifier{
		config: jwtConfig,
		client: &http.Client{Timeout: 10 * time.Second},
		keys:   map[string]any{},
	}, nil
}

func (v *AuthProviderJWTVerifier) Verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
	if v == nil {
		return nil, errors.New("auth provider verifier is not configured")
	}

	if v.config.SigningMethod == authProviderSigningMethodHMAC {
		return v.verifyHMAC(ctx, tokenString)
	}

	return v.verifyJWKS(ctx, tokenString)
}

func (v *AuthProviderJWTVerifier) parserOptions() []jwt.ParserOption {
	options := []jwt.ParserOption{jwt.WithExpirationRequired()}
	if v.config.Issuer != "" {
		options = append(options, jwt.WithIssuer(v.config.Issuer))
	}
	if v.config.Audience != "" {
		options = append(options, jwt.WithAudience(v.config.Audience))
	}

	return options
}
