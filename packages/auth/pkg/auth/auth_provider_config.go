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
	defaultAuthProviderEmailClaim        = "email"

	authProviderSigningMethodJWKS = "JWKS"
	authProviderSigningMethodHMAC = "HMAC"
)

// AuthProviderConfig describes external auth provider verification.
type AuthProviderConfig struct {
	JWT AuthProviderJWTConfig `json:"jwt"`
}

// AuthProviderJWTConfig describes a JWT issuer with JWKS or HMAC signing.
type AuthProviderJWTConfig struct {
	JWKSURL           string        `json:"jwks_url"`
	Issuer            string        `json:"issuer"`
	Audience          string        `json:"audience"`
	SigningMethod     string        `json:"signing_method"`
	HMACSecrets       []string      `json:"hmac_secrets"`
	UserIDClaim       string        `json:"user_id_claim"`
	EmailClaim        string        `json:"email_claim"`
	JWKSCacheDuration time.Duration `json:"jwks_cache_duration"`
}

func (c *AuthProviderJWTConfig) UnmarshalJSON(data []byte) error {
	type authProviderJWTConfigJSON struct {
		JWKSURL           string   `json:"jwks_url"`
		Issuer            string   `json:"issuer"`
		Audience          string   `json:"audience"`
		SigningMethod     string   `json:"signing_method"`
		HMACSecrets       []string `json:"hmac_secrets"`
		UserIDClaim       string   `json:"user_id_claim"`
		EmailClaim        string   `json:"email_claim"`
		JWKSCacheDuration string   `json:"jwks_cache_duration"`
	}

	var raw authProviderJWTConfigJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	c.JWKSURL = raw.JWKSURL
	c.Issuer = raw.Issuer
	c.Audience = raw.Audience
	c.SigningMethod = raw.SigningMethod
	c.HMACSecrets = raw.HMACSecrets
	c.UserIDClaim = raw.UserIDClaim
	c.EmailClaim = raw.EmailClaim

	if raw.JWKSCacheDuration != "" {
		duration, err := time.ParseDuration(raw.JWKSCacheDuration)
		if err != nil {
			return fmt.Errorf("parse jwks_cache_duration: %w", err)
		}

		c.JWKSCacheDuration = duration
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
	return strings.TrimSpace(c.JWKSURL) != "" || len(c.HMACSecrets) > 0
}

func (c AuthProviderJWTConfig) normalized() AuthProviderJWTConfig {
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

func (c AuthProviderJWTConfig) validate() error {
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
		JWT: AuthProviderJWTConfig{
			SigningMethod: authProviderSigningMethodHMAC,
			HMACSecrets:   secrets,
		},
	}
}
