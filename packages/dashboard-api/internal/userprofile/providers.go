package userprofile

import "strings"

const (
	authProviderEmail  = "email"
	authProviderGoogle = "google"
	authProviderGithub = "github"

	oryCredentialOIDC     = "oidc"
	oryCredentialPassword = "password"
)

var (
	authProviderOrder = []string{authProviderEmail, authProviderGoogle, authProviderGithub}

	oryProfileCredentialTypes = []string{
		oryCredentialOIDC,
		oryCredentialPassword,
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
	provider := strings.ToLower(strings.TrimSpace(value))
	switch {
	case provider == authProviderEmail:
		return authProviderEmail
	case provider == authProviderGoogle || strings.HasPrefix(provider, authProviderGoogle+"-"):
		return authProviderGoogle
	case provider == authProviderGithub || strings.HasPrefix(provider, authProviderGithub+"-"):
		return authProviderGithub
	default:
		return ""
	}
}
