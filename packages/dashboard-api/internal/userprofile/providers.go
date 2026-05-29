package userprofile

import "strings"

const (
	authProviderEmail  = "email"
	authProviderGoogle = "google"
	authProviderGithub = "github"

	oryCredentialOIDC     = "oidc"
	oryCredentialPassword = "password"
	oryCredentialCode     = "code"
	oryCredentialWebAuthn = "webauthn"
	oryCredentialPasskey  = "passkey"
)

var (
	authProviderOrder = []string{authProviderEmail, authProviderGoogle, authProviderGithub}

	oryProfileCredentialTypes = []string{
		oryCredentialOIDC,
		oryCredentialPassword,
		oryCredentialCode,
		oryCredentialWebAuthn,
		oryCredentialPasskey,
	}
)

func normalizeAuthProviders(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		provider := normalizeAuthProvider(value)
		if provider == "" {
			continue
		}
		seen[provider] = struct{}{}
	}

	if len(seen) == 0 {
		return nil
	}

	providers := make([]string, 0, len(seen))
	for _, provider := range authProviderOrder {
		if _, ok := seen[provider]; ok {
			providers = append(providers, provider)
		}
	}

	return providers
}

func normalizeAuthProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case authProviderEmail, oryCredentialPassword, oryCredentialCode, oryCredentialWebAuthn, oryCredentialPasskey:
		return authProviderEmail
	case authProviderGoogle:
		return authProviderGoogle
	case authProviderGithub:
		return authProviderGithub
	default:
		return ""
	}
}

func isOryEmailCredentialType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case oryCredentialPassword, oryCredentialCode, oryCredentialWebAuthn, oryCredentialPasskey:
		return true
	default:
		return false
	}
}
