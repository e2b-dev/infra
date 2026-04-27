package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAuthProviderJWKSCacheDuration = 5 * time.Minute
	defaultAuthProviderUserIDClaim       = "sub"
)

// AuthProviderConfig describes external auth provider verification.
type AuthProviderConfig struct {
	JWT AuthProviderJWTConfig `json:"jwt"`
}

// AuthProviderJWTConfig describes a JWT issuer with JWKS or HMAC signing.
type AuthProviderJWTConfig struct {
	Issuer      string                  `json:"issuer"`
	Audience    string                  `json:"audience"`
	UserIDClaim string                  `json:"user_id_claim"`
	JWKS        *AuthProviderJWKSConfig `json:"jwks"`
	HMAC        *AuthProviderHMACConfig `json:"hmac"`
}

type AuthProviderJWKSConfig struct {
	URL           string        `json:"url"`
	CacheDuration time.Duration `json:"cache_duration"`
}

type AuthProviderHMACConfig struct {
	Secrets []string `json:"secrets"`
}

func (c *AuthProviderJWKSConfig) UnmarshalJSON(data []byte) error {
	type authProviderJWKSConfigJSON struct {
		URL           string `json:"url"`
		CacheDuration string `json:"cache_duration"`
	}

	var raw authProviderJWKSConfigJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	c.URL = raw.URL

	if raw.CacheDuration != "" {
		duration, err := time.ParseDuration(raw.CacheDuration)
		if err != nil {
			return fmt.Errorf("parse jwks.cache_duration: %w", err)
		}

		c.CacheDuration = duration
	}

	return nil
}

// Enabled returns true when external auth provider JWT validation is configured.
func (c AuthProviderConfig) Enabled() bool {
	return c.JWT.Enabled()
}

func (c AuthProviderConfig) normalizedJWT() AuthProviderJWTConfig {
	return c.JWT.normalized()
}

func (c AuthProviderConfig) validate() error {
	return c.normalizedJWT().validate()
}

// Enabled returns true when external auth provider JWT validation is configured.
func (c AuthProviderJWTConfig) Enabled() bool {
	return c.JWKS != nil || c.HMAC != nil
}

func (c AuthProviderJWTConfig) normalized() AuthProviderJWTConfig {
	c.Issuer = strings.TrimSpace(c.Issuer)
	c.Audience = strings.TrimSpace(c.Audience)
	c.UserIDClaim = strings.TrimSpace(c.UserIDClaim)

	if c.UserIDClaim == "" {
		c.UserIDClaim = defaultAuthProviderUserIDClaim
	}
	if c.JWKS != nil {
		c.JWKS.URL = strings.TrimSpace(c.JWKS.URL)
		if c.JWKS.CacheDuration <= 0 {
			c.JWKS.CacheDuration = defaultAuthProviderJWKSCacheDuration
		}
	}

	return c
}

func (c AuthProviderJWTConfig) validate() error {
	if !c.Enabled() {
		return nil
	}

	if c.JWKS != nil && c.HMAC != nil {
		return errors.New("auth provider JWT config must specify exactly one of jwks or hmac")
	}

	if c.HMAC != nil {
		if len(c.HMAC.Secrets) == 0 {
			return errors.New("auth provider HMAC secrets are required when hmac is configured")
		}

		return nil
	}

	if c.JWKS == nil {
		return nil
	}

	parsedURL, err := url.ParseRequestURI(c.JWKS.URL)
	if err != nil {
		return fmt.Errorf("invalid auth provider JWKS URL: %w", err)
	}
	if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		return fmt.Errorf("invalid auth provider JWKS URL scheme %q", parsedURL.Scheme)
	}
	if c.Issuer == "" {
		return errors.New("auth provider issuer is required when jwks is configured")
	}

	return nil
}

func NewHMACAuthProviderConfig(secrets []string) AuthProviderConfig {
	return AuthProviderConfig{
		JWT: AuthProviderJWTConfig{
			HMAC: &AuthProviderHMACConfig{
				Secrets: secrets,
			},
		},
	}
}
