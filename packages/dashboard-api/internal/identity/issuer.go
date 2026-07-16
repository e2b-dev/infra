package identity

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
)

// ResolveOryIssuer picks the Ory issuer URL from the auth provider's JWT
// configurations by matching the host of sdkURL against each issuer URL.
// When exactly one JWT entry is configured, its issuer is used without
// requiring a host match.
// When no JWT entries are configured, it falls back to the SDK URL.
func ResolveOryIssuer(sdkURL string, jwtConfigs []oidc.Config) (string, error) {
	sdkURL = strings.TrimSpace(sdkURL)
	issuers := uniqueIssuerURLs(jwtConfigs)

	if len(issuers) == 0 {
		if sdkURL == "" {
			return "", errors.New("ORY_SDK_URL is empty and no JWT issuers are configured in AUTH_PROVIDER_CONFIG")
		}

		return sdkURL, nil
	}

	if len(issuers) == 1 {
		return issuers[0], nil
	}

	sdkHost, err := hostOf(sdkURL)
	if err != nil {
		return "", fmt.Errorf("parse ORY_SDK_URL: %w", err)
	}

	for _, issuer := range issuers {
		issuerHost, err := hostOf(issuer)
		if err != nil {
			continue
		}
		if issuerHost == sdkHost {
			return issuer, nil
		}
	}

	return "", fmt.Errorf("no JWT issuer in AUTH_PROVIDER_CONFIG matches ORY_SDK_URL host %q", sdkHost)
}

func uniqueIssuerURLs(jwtConfigs []oidc.Config) []string {
	seen := make(map[string]struct{}, len(jwtConfigs))
	issuers := make([]string, 0, len(jwtConfigs))
	for _, jwt := range jwtConfigs {
		issuer := strings.TrimSpace(jwt.Issuer.URL)
		if issuer == "" {
			continue
		}
		if _, ok := seen[issuer]; ok {
			continue
		}
		seen[issuer] = struct{}{}
		issuers = append(issuers, issuer)
	}

	return issuers
}

func hostOf(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	return parsed.Host, nil
}
