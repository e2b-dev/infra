package userprofile

import (
	"strings"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

const (
	signupIPMetadataKey        = "signup_ip"
	signupUserAgentMetadataKey = "signup_user_agent"
)

func creatorContextFromMetadata(metadata map[string]any, providerNames []string) *sharedteamprovision.CreatorContextV1 {
	return &sharedteamprovision.CreatorContextV1{
		IPAddress:  metadataString(metadata, signupIPMetadataKey),
		UserAgent:  metadataString(metadata, signupUserAgentMetadataKey),
		AuthMethod: authMethodFromProviderNames(providerNames),
	}
}

func authMethodFromProviderNames(providerNames []string) string {
	for _, provider := range providerNames {
		provider = strings.TrimSpace(provider)
		if provider != "" && !strings.EqualFold(provider, authProviderEmail) {
			return sharedteamprovision.AuthMethodSocial
		}
	}

	return sharedteamprovision.AuthMethodPassword
}
