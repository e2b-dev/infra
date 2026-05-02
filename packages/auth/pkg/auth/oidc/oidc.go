package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
)

// JWKSHTTPTimeout is the timeout used for OIDC discovery and JWKS HTTP
// requests.
const JWKSHTTPTimeout = 10 * time.Second

// Verifier verifies JWTs against a single OIDC issuer.
type Verifier struct {
	keyfunc       keyfunc.Keyfunc
	userIDClaim   string
	audiences     []string
	parserOptions []jwt.ParserOption
}

// discoveryDocument is a minimal subset of the OIDC discovery document
// (https://openid.net/specs/openid-connect-discovery-1_0.html#ProviderMetadata).
type discoveryDocument struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// NewVerifier constructs a Verifier from the supplied Entry. It performs the
// OIDC discovery fetch synchronously and fails fast on configuration or
// network errors.
func NewVerifier(ctx context.Context, entry Entry, httpClient *http.Client) (*Verifier, error) {
	if httpClient == nil {
		return nil, errors.New("OIDC JWKS HTTP client is required")
	}

	if entry.Issuer.URL == "" {
		return nil, errors.New("issuer URL is required")
	}

	discoveryURL := entry.DiscoveryURL()
	if err := validateHTTPSURL(discoveryURL, "discoveryURL"); err != nil {
		return nil, err
	}

	doc, err := fetchDiscoveryDocument(ctx, httpClient, discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("fetch OIDC discovery document at %s: %w", discoveryURL, err)
	}

	if doc.Issuer != entry.Issuer.URL {
		return nil, fmt.Errorf("discovery document issuer %q does not match configured issuer %q", doc.Issuer, entry.Issuer.URL)
	}

	if err := validateHTTPSURL(doc.JWKSURI, "discovery jwks_uri"); err != nil {
		return nil, err
	}

	storage, err := jwkset.NewStorageFromHTTP(doc.JWKSURI, jwkset.HTTPClientStorageOptions{
		Client:          httpClient,
		Ctx:             ctx,
		HTTPTimeout:     JWKSHTTPTimeout,
		RefreshInterval: entry.JWKSCacheDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("create OIDC JWKS storage: %w", err)
	}

	keyFunc, err := keyfunc.New(keyfunc.Options{
		Ctx:     ctx,
		Storage: storage,
	})
	if err != nil {
		return nil, fmt.Errorf("create OIDC JWKS keyfunc: %w", err)
	}

	parserOptions := []jwt.ParserOption{
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(entry.Issuer.URL),
	}

	return &Verifier{
		keyfunc:       keyFunc,
		userIDClaim:   entry.ClaimMappings.Username.Claim,
		audiences:     entry.Issuer.Audiences,
		parserOptions: parserOptions,
	}, nil
}

// Verify parses and validates the supplied token string.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (*jwtutil.Identity, error) {
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

	if err := jwtutil.ValidateAudience(claims, v.audiences); err != nil {
		return nil, fmt.Errorf("failed to verify auth provider token: %w", err)
	}

	return jwtutil.IdentityFromClaims(claims, v.userIDClaim), nil
}

func fetchDiscoveryDocument(ctx context.Context, httpClient *http.Client, discoveryURL string) (*discoveryDocument, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, JWKSHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute discovery request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

		return nil, fmt.Errorf("discovery endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var doc discoveryDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode discovery document: %w", err)
	}

	if doc.Issuer == "" {
		return nil, errors.New("discovery document is missing issuer")
	}

	if doc.JWKSURI == "" {
		return nil, errors.New("discovery document is missing jwks_uri")
	}

	return &doc, nil
}

// validateHTTPSURL applies the same checks as validateURL but returns at the
// first failure. It is intended for runtime validation of URLs derived from
// the OIDC discovery document where one error is enough to fail fast.
func validateHTTPSURL(rawURL string, field string) error {
	if errs := validateURL(rawURL, field); len(errs) > 0 {
		return errs[0]
	}

	return nil
}

// validateURL enforces the same constraints Kubernetes requires of issuer
// URLs:
//   - parseable via url.Parse
//   - https scheme
//   - no userinfo (username/password)
//   - no query string
//   - no fragment
//
// The field name is included in error messages to help operators locate the
// offending value. All applicable errors are returned together.
func validateURL(rawURL string, field string) []error {
	var errs []error

	u, err := url.Parse(rawURL)
	if err != nil {
		return []error{fmt.Errorf("invalid %s: %w", field, err)}
	}
	if u.Scheme != "https" {
		errs = append(errs, fmt.Errorf("invalid %s scheme %q (must be https)", field, u.Scheme))
	}
	if u.User != nil {
		errs = append(errs, fmt.Errorf("invalid %s: must not contain a username or password", field))
	}
	if len(u.RawQuery) > 0 {
		errs = append(errs, fmt.Errorf("invalid %s: must not contain a query", field))
	}
	if len(u.Fragment) > 0 {
		errs = append(errs, fmt.Errorf("invalid %s: must not contain a fragment", field))
	}

	return errs
}

// validateIssuerURL enforces presence + URL constraints on the issuer URL.
func validateIssuerURL(issuerURL string) []error {
	if issuerURL == "" {
		return []error{errors.New("issuer.url is required")}
	}

	return validateURL(issuerURL, "issuer.url")
}

// validateDiscoveryURL enforces URL constraints on the optional discovery
// URL plus the requirement that it differ from the issuer URL when set.
func validateDiscoveryURL(issuerURL, discoveryURL string) []error {
	if discoveryURL == "" {
		return nil
	}

	var errs []error
	if issuerURL != "" && strings.TrimRight(issuerURL, "/") == strings.TrimRight(discoveryURL, "/") {
		errs = append(errs, errors.New("issuer.discoveryURL must be different from issuer.url"))
	}
	errs = append(errs, validateURL(discoveryURL, "issuer.discoveryURL")...)

	return errs
}
