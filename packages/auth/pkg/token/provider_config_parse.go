package token

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseProviderConfig parses a provider-config env value (AUTH_PROVIDER_CONFIG,
// ADMIN_AUTH_CONFIG) into a ProviderConfig. Empty input and the literal string
// "null" (with surrounding whitespace) both produce a zero-value ProviderConfig
// with no error, so that Terraform `jsonencode(null)` values and unset env vars
// behave the same.
func ParseProviderConfig(v string) (ProviderConfig, error) {
	var config ProviderConfig
	trimmed := strings.TrimSpace(v)
	if trimmed == "" || trimmed == "null" {
		return config, nil
	}

	if err := json.Unmarshal([]byte(v), &config); err != nil {
		return ProviderConfig{}, fmt.Errorf("parse auth provider config: %w", err)
	}

	return config, nil
}
