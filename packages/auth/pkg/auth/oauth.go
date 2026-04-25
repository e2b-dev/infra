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
	defaultAuthProviderJWKSCacheDuration = 5 * time.Minute
	defaultAuthProviderUserIDClaim       = "sub"
	defaultAuthProviderEmailClaim        = "email"
)

// AuthProviderConfig describes a generic OAuth/OIDC JWT issuer backed by JWKS.
type AuthProviderConfig struct {
	JWKSURL           string        `env:"AUTH_PROVIDER_JWKS_URL"`
	Issuer            string        `env:"AUTH_PROVIDER_JWT_ISSUER"`
	Audience          string        `env:"AUTH_PROVIDER_JWT_AUDIENCE"`
	UserIDClaim       string        `env:"AUTH_PROVIDER_JWT_USER_ID_CLAIM"   envDefault:"sub"`
	EmailClaim        string        `env:"AUTH_PROVIDER_JWT_EMAIL_CLAIM"     envDefault:"email"`
	JWKSCacheDuration time.Duration `env:"AUTH_PROVIDER_JWKS_CACHE_DURATION" envDefault:"5m"`
}

// Enabled returns true when external auth provider JWT validation is configured.
func (c AuthProviderConfig) Enabled() bool {
	return strings.TrimSpace(c.JWKSURL) != ""
}

func (c AuthProviderConfig) normalized() AuthProviderConfig {
	c.JWKSURL = strings.TrimSpace(c.JWKSURL)
	c.Issuer = strings.TrimSpace(c.Issuer)
	c.Audience = strings.TrimSpace(c.Audience)
	c.UserIDClaim = strings.TrimSpace(c.UserIDClaim)
	c.EmailClaim = strings.TrimSpace(c.EmailClaim)

	if c.UserIDClaim == "" {
		c.UserIDClaim = defaultAuthProviderUserIDClaim
	}
	if c.EmailClaim == "" {
		c.EmailClaim = defaultAuthProviderEmailClaim
	}
	if c.JWKSCacheDuration <= 0 {
		c.JWKSCacheDuration = defaultAuthProviderJWKSCacheDuration
	}

	return c
}

func (c AuthProviderConfig) validate() error {
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

// AuthProviderIdentity is the normalized identity extracted from a validated auth provider JWT.
type AuthProviderIdentity struct {
	UserID uuid.UUID
	Email  string
	Claims jwt.MapClaims
}

type AuthProviderJWTVerifier struct {
	config AuthProviderConfig
	client *http.Client

	mu        sync.RWMutex
	keys      map[string]any
	expiresAt time.Time
}

func NewAuthProviderJWTVerifier(config AuthProviderConfig) (*AuthProviderJWTVerifier, error) {
	config = config.normalized()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if !config.Enabled() {
		return nil, nil
	}

	return &AuthProviderJWTVerifier{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
		keys:   map[string]any{},
	}, nil
}

func (v *AuthProviderJWTVerifier) Verify(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
	if v == nil {
		return nil, errors.New("auth provider verifier is not configured")
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
		return nil, fmt.Errorf("failed to verify auth provider token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("auth provider token is invalid")
	}

	identity := &AuthProviderIdentity{Claims: claims}
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

func (v *AuthProviderJWTVerifier) keyForToken(ctx context.Context, token *jwt.Token) (any, error) {
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

func (v *AuthProviderJWTVerifier) cachedKey(kid string) (any, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if time.Now().After(v.expiresAt) {
		return nil, false
	}

	key, ok := v.keys[kid]

	return key, ok
}

func (v *AuthProviderJWTVerifier) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.config.JWKSURL, nil)
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
	v.expiresAt = time.Now().Add(v.config.JWKSCacheDuration)
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
