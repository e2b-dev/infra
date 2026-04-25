package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	defaultOAuthCacheDuration = 5 * time.Minute
	defaultOAuthUserIDClaim   = "sub"
	defaultOAuthEmailClaim    = "email"
)

// OAuthConfig describes a generic OAuth/OIDC JWT issuer backed by JWKS.
type OAuthConfig struct {
	JWKSURL       string
	Issuer        string
	Audience      string
	UserIDClaim   string
	EmailClaim    string
	CacheDuration time.Duration
}

// Enabled returns true when OAuth/JWKS token validation is configured.
func (c OAuthConfig) Enabled() bool {
	return strings.TrimSpace(c.JWKSURL) != ""
}

func (c OAuthConfig) normalized() OAuthConfig {
	c.JWKSURL = strings.TrimSpace(c.JWKSURL)
	c.Issuer = strings.TrimSpace(c.Issuer)
	c.Audience = strings.TrimSpace(c.Audience)
	c.UserIDClaim = strings.TrimSpace(c.UserIDClaim)
	c.EmailClaim = strings.TrimSpace(c.EmailClaim)

	if c.UserIDClaim == "" {
		c.UserIDClaim = defaultOAuthUserIDClaim
	}
	if c.EmailClaim == "" {
		c.EmailClaim = defaultOAuthEmailClaim
	}
	if c.CacheDuration <= 0 {
		c.CacheDuration = defaultOAuthCacheDuration
	}

	return c
}

func (c OAuthConfig) validate() error {
	if !c.Enabled() {
		return nil
	}

	parsedURL, err := url.ParseRequestURI(c.JWKSURL)
	if err != nil {
		return fmt.Errorf("invalid OAuth JWKS URL: %w", err)
	}
	if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		return fmt.Errorf("invalid OAuth JWKS URL scheme %q", parsedURL.Scheme)
	}
	if c.Issuer == "" {
		return errors.New("OAuth issuer is required when OAuth JWKS URL is configured")
	}

	return nil
}

// OAuthIdentity is the normalized identity extracted from a validated OAuth JWT.
type OAuthIdentity struct {
	UserID uuid.UUID
	Email  string
	Claims jwt.MapClaims
}

type OAuthJWTVerifier struct {
	config OAuthConfig
	client *http.Client

	mu        sync.RWMutex
	keys      map[string]any
	expiresAt time.Time
}

func NewOAuthJWTVerifier(config OAuthConfig) (*OAuthJWTVerifier, error) {
	config = config.normalized()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if !config.Enabled() {
		return nil, nil
	}

	return &OAuthJWTVerifier{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
		keys:   map[string]any{},
	}, nil
}

func (v *OAuthJWTVerifier) Verify(ctx context.Context, tokenString string) (*OAuthIdentity, error) {
	if v == nil {
		return nil, errors.New("OAuth verifier is not configured")
	}

	claims := jwt.MapClaims{}
	options := []jwt.ParserOption{
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(v.config.Issuer),
	}
	if v.config.Audience != "" {
		options = append(options, jwt.WithAudience(v.config.Audience))
	}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return v.keyForToken(ctx, token)
	}, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to verify OAuth token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("OAuth token is invalid")
	}

	identity := &OAuthIdentity{Claims: claims}
	if claimValue, ok := claimString(claims, v.config.UserIDClaim); ok {
		userID, err := uuid.Parse(claimValue)
		if err == nil {
			identity.UserID = userID
		}
	}
	if email, ok := claimString(claims, v.config.EmailClaim); ok {
		identity.Email = email
	}

	return identity, nil
}

func (v *OAuthJWTVerifier) keyForToken(ctx context.Context, token *jwt.Token) (any, error) {
	switch token.Method.(type) {
	case *jwt.SigningMethodRSA, *jwt.SigningMethodRSAPSS, *jwt.SigningMethodECDSA:
	default:
		return nil, fmt.Errorf("unexpected OAuth signing method: %v", token.Header["alg"])
	}

	kid, ok := token.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, errors.New("OAuth token is missing kid header")
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

	return nil, fmt.Errorf("OAuth signing key %q not found", kid)
}

func (v *OAuthJWTVerifier) cachedKey(kid string) (any, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if time.Now().After(v.expiresAt) {
		return nil, false
	}

	key, ok := v.keys[kid]

	return key, ok
}

func (v *OAuthJWTVerifier) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.config.JWKSURL, nil)
	if err != nil {
		return fmt.Errorf("create JWKS request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch OAuth JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("fetch OAuth JWKS: unexpected status %d", resp.StatusCode)
	}

	var set jose.JSONWebKeySet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("decode OAuth JWKS: %w", err)
	}

	keys := make(map[string]any, len(set.Keys))
	for _, key := range set.Keys {
		if key.KeyID == "" || key.Key == nil {
			continue
		}

		keys[key.KeyID] = key.Key
	}
	if len(keys) == 0 {
		return errors.New("OAuth JWKS contains no usable keys")
	}

	v.mu.Lock()
	v.keys = keys
	v.expiresAt = time.Now().Add(v.config.CacheDuration)
	v.mu.Unlock()

	return nil
}

func claimString(claims jwt.MapClaims, name string) (string, bool) {
	value, ok := claims[name]
	if !ok {
		return "", false
	}

	switch typed := value.(type) {
	case string:
		return typed, typed != ""
	case []string:
		if len(typed) == 0 {
			return "", false
		}

		return typed[0], typed[0] != ""
	case []any:
		if len(typed) == 0 {
			return "", false
		}
		first, ok := typed[0].(string)

		return first, ok && first != ""
	default:
		return "", false
	}
}
