package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// oidcHTTPTimeout is the timeout used for OIDC discovery and JWKS HTTP
// requests.
const oidcHTTPTimeout = 10 * time.Second

// ErrIdentityNotFound is returned by Verify when the token is valid but no
// matching row exists in public.user_identities for (iss, sub).
var ErrIdentityNotFound = errors.New("oidc identity not found")

// IdentityLookup resolves the internal user UUID for an OIDC identity
// (issuer + subject). Implementations should return ErrIdentityNotFound when
// no row matches the supplied pair.
type IdentityLookup interface {
	GetUserIdentity(ctx context.Context, iss, sub string) (uuid.UUID, error)
}

// Verifier verifies JWTs against a single OIDC issuer.
type Verifier struct {
	keyfunc       keyfunc.Keyfunc
	audiences     []string
	parserOptions []jwt.ParserOption
	identities    IdentityLookup
}

// discoveryDocument is a minimal subset of the OIDC discovery document
// (https://openid.net/specs/openid-connect-discovery-1_0.html#ProviderMetadata).
type discoveryDocument struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// NewVerifier constructs a Verifier from the supplied Config. It performs the
// OIDC discovery fetch synchronously and fails fast on configuration or
// network errors.
func NewVerifier(ctx context.Context, entry Config, httpClient *http.Client, identities IdentityLookup) (*Verifier, error) {
	if httpClient == nil {
		return nil, errors.New("OIDC JWKS HTTP client is required")
	}

	if identities == nil {
		return nil, errors.New("OIDC identity lookup is required")
	}

	if entry.Issuer.URL == "" {
		return nil, errors.New("issuer URL is required")
	}

	discoveryURL := entry.discoveryURL()
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
		HTTPTimeout:     oidcHTTPTimeout,
		RefreshInterval: entry.CacheDuration,
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

	return &Verifier{
		keyfunc:   keyFunc,
		audiences: entry.Issuer.Audiences,
		parserOptions: []jwt.ParserOption{
			jwt.WithExpirationRequired(),
			jwt.WithIssuer(entry.Issuer.URL),
		},
		identities: identities,
	}, nil
}

// Verify parses and validates the supplied token string and resolves the
// internal user UUID for the (iss, sub) pair via the configured
// IdentityLookup. When the token is valid but no matching identity exists,
// the returned error wraps ErrIdentityNotFound.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (uuid.UUID, jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return v.keyfunc.KeyfuncCtx(ctx)(token)
	}, v.parserOptions...)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to verify auth provider token: %w", err)
	}
	if !token.Valid {
		return uuid.Nil, nil, errors.New("auth provider token is invalid")
	}

	if err := validateAudience(claims, v.audiences); err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to verify auth provider token: %w", err)
	}

	iss, ok := claimString(claims, "iss")
	if !ok {
		return uuid.Nil, nil, errors.New("auth provider token is missing iss claim")
	}

	sub, ok := claimString(claims, "sub")
	if !ok {
		return uuid.Nil, nil, errors.New("auth provider token is missing sub claim")
	}

	userID, err := v.identities.GetUserIdentity(ctx, iss, sub)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("resolve user identity for auth provider token: %w", err)
	}

	return userID, claims, nil
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

func fetchDiscoveryDocument(ctx context.Context, httpClient *http.Client, discoveryURL string) (*discoveryDocument, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, oidcHTTPTimeout)
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
//   - https scheme (with one exception, see isLoopbackHost below)
//   - no userinfo (username/password)
//   - no query string
//   - no fragment
//
// Loopback exception: an http:// URL is accepted when its host resolves to
// a loopback address (`localhost`, `127.0.0.1`, `[::1]`). Any non-loopback host
// still requires https.
//
// The field name is included in error messages to help operators locate the
// offending value. All applicable errors are returned together.
func validateURL(rawURL string, field string) []error {
	var errs []error

	u, err := url.Parse(rawURL)
	if err != nil {
		return []error{fmt.Errorf("invalid %s: %w", field, err)}
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopbackHost(u.Hostname())) {
		errs = append(errs, fmt.Errorf("invalid %s scheme %q (must be https, or http for loopback hosts)", field, u.Scheme))
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

// isLoopbackHost reports whether the given URL host is a loopback address.
// It accepts:
//   - the literal name "localhost" (case-insensitive)
//   - any IPv4 address in 127.0.0.0/8
//   - the IPv6 loopback ::1 (Hostname() strips the brackets, so we receive "::1")
//
// We deliberately *do not* resolve the name via DNS: that would make
// validation depend on host resolver state and turn this into a TOCTOU
// surface. Matching string-and-literal-IP only is enough for the local-dev
// use case and keeps the check pure.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
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
