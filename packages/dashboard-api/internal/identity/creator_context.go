package identity

import (
	"strings"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

const (
	signupIPMetadataKey        = "signup_ip"
	signupUserAgentMetadataKey = "signup_user_agent"
)

func authMethodFromProviderNames(providerNames []string) string {
	for _, provider := range providerNames {
		provider = strings.TrimSpace(provider)
		if provider != "" && !strings.EqualFold(provider, authProviderEmail) {
			return sharedteamprovision.AuthMethodSocial
		}
	}

	return sharedteamprovision.AuthMethodPassword
}
