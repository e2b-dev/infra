package fc

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

// The metadata serialization should not be changed — it is different from the field names we use here!
type MmdsMetadata struct {
	SandboxID  string `json:"instanceID"`
	TemplateID string `json:"envID"`

	LogsCollectorAddress string `json:"address"`
	AccessTokenHash      string `json:"accessTokenHash,omitempty"`
}

// HashAccessToken computes the SHA-512 hash of an access token.
// Deprecated: Use keys.HashAccessToken from shared package instead.
func HashAccessToken(token string) string {
	return keys.HashAccessToken(token)
}
