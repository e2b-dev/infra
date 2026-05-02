package oidc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
)

const (
	// DefaultJWKSCacheDuration is the default JWKS cache duration applied
	// when an Entry does not specify one.
	DefaultJWKSCacheDuration = 5 * time.Minute
	// DefaultDiscoveryPath is the relative path appended to the issuer URL
	// to derive the discovery URL when one is not explicitly configured.
	DefaultDiscoveryPath = "/.well-known/openid-configuration"
)

// Entry describes a single OIDC issuer.
type Entry struct {
	Issuer            Issuer                `json:"issuer"`
	ClaimMappings     jwtutil.ClaimMappings `json:"claimMappings"`
	JWKSCacheDuration time.Duration         `json:"jwksCacheDuration"`
}

// Issuer describes an OIDC issuer endpoint plus audience policy.
type Issuer struct {
	URL                 string                      `json:"url"`
	DiscoveryURL        string                      `json:"discoveryURL"`
	Audiences           []string                    `json:"audiences"`
	AudienceMatchPolicy jwtutil.AudienceMatchPolicy `json:"audienceMatchPolicy"`
}

// UnmarshalJSON parses `jwksCacheDuration` from a Go duration string.
func (e *Entry) UnmarshalJSON(data []byte) error {
	type entryJSON struct {
		Issuer            Issuer                `json:"issuer"`
		ClaimMappings     jwtutil.ClaimMappings `json:"claimMappings"`
		JWKSCacheDuration string                `json:"jwksCacheDuration"`
	}

	var raw entryJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	e.Issuer = raw.Issuer
	e.ClaimMappings = raw.ClaimMappings

	if raw.JWKSCacheDuration != "" {
		duration, err := time.ParseDuration(raw.JWKSCacheDuration)
		if err != nil {
			return fmt.Errorf("parse jwksCacheDuration: %w", err)
		}

		e.JWKSCacheDuration = duration
	}

	return nil
}

// Normalized returns a copy with defaults applied.
func (e *Entry) Normalized() Entry {
	out := *e
	out.Issuer.URL = strings.TrimSpace(out.Issuer.URL)
	out.Issuer.DiscoveryURL = strings.TrimSpace(out.Issuer.DiscoveryURL)
	out.ClaimMappings = out.ClaimMappings.Normalized()

	if out.JWKSCacheDuration <= 0 {
		out.JWKSCacheDuration = DefaultJWKSCacheDuration
	}

	return out
}

// Validate returns an error if the entry contains invalid configuration.
//
// All issues found are joined into a single error to surface as much useful
// feedback as possible in one pass.
func (e *Entry) Validate() error {
	var errs []error

	errs = append(errs, validateIssuerURL(e.Issuer.URL)...)
	errs = append(errs, validateDiscoveryURL(e.Issuer.URL, e.Issuer.DiscoveryURL)...)

	if err := jwtutil.ValidateAudienceMatchPolicy(e.Issuer.AudienceMatchPolicy, e.Issuer.Audiences); err != nil {
		errs = append(errs, fmt.Errorf("issuer: %w", err))
	}

	return errors.Join(errs...)
}

// DiscoveryURL returns the configured discoveryURL or the default derived
// from the issuer URL.
func (e *Entry) DiscoveryURL() string {
	if e.Issuer.DiscoveryURL != "" {
		return e.Issuer.DiscoveryURL
	}

	return strings.TrimRight(e.Issuer.URL, "/") + DefaultDiscoveryPath
}
