package userprofile

import (
	"strings"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	signupIPMetadataKey        = "signup_ip"
	signupUserAgentMetadataKey = "signup_user_agent"
	ipMetadataKey              = "ip"
	ipAddressMetadataKey       = "ip_address"
	userAgentMetadataKey       = "user_agent"
	providersMetadataKey       = "providers"
	providerMetadataKey        = "provider"
)

func creatorContextFromMetadata(metadata map[string]any, providerNames []string) *sharedteamprovision.CreatorContextV1 {
	return &sharedteamprovision.CreatorContextV1{
		IPAddress: utils.FirstNonEmpty(
			metadataString(metadata, signupIPMetadataKey),
			metadataString(metadata, ipAddressMetadataKey),
			metadataString(metadata, ipMetadataKey),
		),
		UserAgent: utils.FirstNonEmpty(
			metadataString(metadata, signupUserAgentMetadataKey),
			metadataString(metadata, userAgentMetadataKey),
		),
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

func providerNamesFromSupabaseMetadata(metadata map[string]any) []string {
	if metadata == nil {
		return nil
	}

	providers := make([]string, 0, 4)
	if list, ok := metadata[providersMetadataKey].([]any); ok {
		for _, entry := range list {
			if name, ok := entry.(string); ok {
				providers = append(providers, name)
			}
		}
	}
	if name, ok := metadata[providerMetadataKey].(string); ok {
		providers = append(providers, name)
	}

	return providers
}
