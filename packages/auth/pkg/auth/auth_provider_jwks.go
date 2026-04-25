package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

type jwksAuthProviderJWTVerifier struct {
	jwksURL           string
	userIDClaim       string
	emailClaim        string
	jwksCacheDuration time.Duration
	parserOptions     []jwt.ParserOption
	client            *http.Client

	mu        sync.RWMutex
	keys      map[string]any
	expiresAt time.Time
}

func newJWKSAuthProviderJWTVerifier(config AuthProviderJWTConfig) *jwksAuthProviderJWTVerifier {
	return &jwksAuthProviderJWTVerifier{
		jwksURL:           config.JWKSURL,
		userIDClaim:       config.UserIDClaim,
		emailClaim:        config.EmailClaim,
		jwksCacheDuration: config.JWKSCacheDuration,
		parserOptions:     authProviderJWTParserOptions(config.Issuer, config.Audience),
		client:            &http.Client{Timeout: 10 * time.Second},
		keys:              map[string]any{},
	}
}

func (v *jwksAuthProviderJWTVerifier) verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return v.keyForToken(ctx, token)
	}, v.parserOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to verify auth provider token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("auth provider token is invalid")
	}

	return identityFromClaims(claims, v.userIDClaim, v.emailClaim), nil
}

func (v *jwksAuthProviderJWTVerifier) keyForToken(ctx context.Context, token *jwt.Token) (any, error) {
	switch token.Method.(type) {
	case *jwt.SigningMethodRSA, *jwt.SigningMethodRSAPSS, *jwt.SigningMethodECDSA:
	default:
		return nil, fmt.Errorf("unexpected auth provider signing method: %v", token.Header["alg"])
	}

	kid, ok := token.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, errors.New("auth provider token is missing kid header")
	}

	if key, ok := v.cachedKey(kid); ok {
		return key, nil
	}
	if err := v.refreshKeys(ctx); err != nil {
		return nil, err
	}
	if key, ok := v.cachedKey(kid); ok {
		return key, nil
	}

	return nil, fmt.Errorf("auth provider signing key %q not found", kid)
}

func (v *jwksAuthProviderJWTVerifier) cachedKey(kid string) (any, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if time.Now().After(v.expiresAt) {
		return nil, false
	}

	key, ok := v.keys[kid]

	return key, ok
}

func (v *jwksAuthProviderJWTVerifier) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("create JWKS request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch auth provider JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("fetch auth provider JWKS: unexpected status %d", resp.StatusCode)
	}

	var set jose.JSONWebKeySet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("decode auth provider JWKS: %w", err)
	}

	keys := make(map[string]any, len(set.Keys))
	for _, key := range set.Keys {
		if key.KeyID == "" || key.Key == nil {
			continue
		}

		keys[key.KeyID] = key.Key
	}
	if len(keys) == 0 {
		return errors.New("auth provider JWKS contains no usable keys")
	}

	v.mu.Lock()
	v.keys = keys
	v.expiresAt = time.Now().Add(v.jwksCacheDuration)
	v.mu.Unlock()

	return nil
}
