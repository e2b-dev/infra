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
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	defaultAuthProviderJWKSCacheDuration = 5 * time.Minute
	defaultAuthProviderUserIDClaim       = "sub"
	defaultAuthProviderEmailClaim        = "email"

	authProviderSigningMethodJWKS = "JWKS"
	authProviderSigningMethodHMAC = "HMAC"
)

// AuthProviderConfig describes a generic OAuth/OIDC JWT issuer.
type AuthProviderConfig struct {
	JWKSURL           string        `env:"AUTH_PROVIDER_JWKS_URL"`
	Issuer            string        `env:"AUTH_PROVIDER_JWT_ISSUER"`
	Audience          string        `env:"AUTH_PROVIDER_JWT_AUDIENCE"`
	SigningMethod     string        `env:"AUTH_PROVIDER_JWT_SIGNING_METHOD"    envDefault:"JWKS"`
	HMACSecrets       []string      `env:"AUTH_PROVIDER_JWT_HMAC_SECRETS"`
	UserIDClaim       string        `env:"AUTH_PROVIDER_JWT_USER_ID_CLAIM"   envDefault:"sub"`
	EmailClaim        string        `env:"AUTH_PROVIDER_JWT_EMAIL_CLAIM"     envDefault:"email"`
	JWKSCacheDuration time.Duration `env:"AUTH_PROVIDER_JWKS_CACHE_DURATION" envDefault:"5m"`
}

// Enabled returns true when external auth provider JWT validation is configured.
func (c AuthProviderConfig) Enabled() bool {
	return strings.TrimSpace(c.JWKSURL) != "" || len(c.HMACSecrets) > 0
}

func (c AuthProviderConfig) normalized() AuthProviderConfig {
	c.JWKSURL = strings.TrimSpace(c.JWKSURL)
	c.Issuer = strings.TrimSpace(c.Issuer)
	c.Audience = strings.TrimSpace(c.Audience)
	c.SigningMethod = strings.ToUpper(strings.TrimSpace(c.SigningMethod))
	c.UserIDClaim = strings.TrimSpace(c.UserIDClaim)
	c.EmailClaim = strings.TrimSpace(c.EmailClaim)

	if c.SigningMethod == "" {
		c.SigningMethod = authProviderSigningMethodJWKS
	}
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

	switch c.SigningMethod {
	case authProviderSigningMethodHMAC:
		if len(c.HMACSecrets) == 0 {
			return errors.New("auth provider HMAC secrets are required when HMAC signing is configured")
		}

		return nil

	case authProviderSigningMethodJWKS:
	default:
		return fmt.Errorf("unknown auth provider JWT signing method %q", c.SigningMethod)
	}

	parsedURL, err := url.ParseRequestURI(c.JWKSURL)
	if err != nil {
		return fmt.Errorf("invalid auth provider JWKS URL: %w", err)
	}
	if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		return fmt.Errorf("invalid auth provider JWKS URL scheme %q", parsedURL.Scheme)
	}
	if c.Issuer == "" {
		return errors.New("auth provider issuer is required when JWKS signing is configured")
	}

	return nil
}

func NewHMACAuthProviderConfig(secrets []string) AuthProviderConfig {
	return AuthProviderConfig{
		SigningMethod: authProviderSigningMethodHMAC,
		HMACSecrets:   secrets,
	}
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

	if v.config.SigningMethod == authProviderSigningMethodHMAC {
		return v.verifyHMAC(ctx, tokenString)
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

	return identityFromClaims(claims, v.config.UserIDClaim, v.config.EmailClaim), nil
}

func (v *AuthProviderJWTVerifier) verifyHMAC(ctx context.Context, tokenString string) (*AuthProviderIdentity, error) {
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
		}, v.parserOptions()...)
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

func identityFromClaims(claims jwt.MapClaims, userIDClaim, emailClaim string) *AuthProviderIdentity {
	identity := &AuthProviderIdentity{Claims: claims}
	if claimValue, ok := claimString(claims, userIDClaim); ok {
		userID, err := uuid.Parse(claimValue)
		if err == nil {
			identity.UserID = userID
		}
	}
	if email, ok := claimString(claims, emailClaim); ok {
		identity.Email = email
	}

	return identity
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
