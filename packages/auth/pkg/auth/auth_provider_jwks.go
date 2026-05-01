package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

const authProviderJWKSHTTPTimeout = 10 * time.Second

type jwksAuthProviderJWTVerifier struct {
	keyfunc       keyfunc.Keyfunc
	userIDClaim   string
	parserOptions []jwt.ParserOption
}

func newJWKSAuthProviderJWTVerifier(ctx context.Context, config AuthProviderJWTConfig, jwksConfig AuthProviderJWKSConfig, httpClient *http.Client) (*jwksAuthProviderJWTVerifier, error) {
	if httpClient == nil {
		return nil, errors.New("auth provider JWKS HTTP client is required")
	}

	storage, err := jwkset.NewStorageFromHTTP(jwksConfig.URL, jwkset.HTTPClientStorageOptions{
		Client:          httpClient,
		Ctx:             ctx,
		HTTPTimeout:     authProviderJWKSHTTPTimeout,
		RefreshInterval: jwksConfig.CacheDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("create auth provider JWKS storage: %w", err)
	}

	keyFunc, err := keyfunc.New(keyfunc.Options{
		Ctx:     ctx,
		Storage: storage,
	})
	if err != nil {
		return nil, fmt.Errorf("create auth provider JWKS keyfunc: %w", err)
	}

	return &jwksAuthProviderJWTVerifier{
		keyfunc:       keyFunc,
		userIDClaim:   config.UserIDClaim,
		parserOptions: authProviderJWTParserOptions(config.Issuer, config.Audience),
	}, nil
}

func (v *jwksAuthProviderJWTVerifier) close() {}

func (v *jwksAuthProviderJWTVerifier) verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return v.keyfunc.KeyfuncCtx(ctx)(token)
	}, v.parserOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to verify auth provider token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("auth provider token is invalid")
	}

	return identityFromClaims(claims, v.userIDClaim), nil
}
