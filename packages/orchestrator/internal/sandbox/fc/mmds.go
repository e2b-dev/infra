package fc

import (
	"crypto/sha512"
	"encoding/hex"
)

// The metadata serialization should not be changed — it is different from the field names we use here!
type MmdsMetadata struct {
	SandboxID  string `json:"instanceID"`
	TemplateID string `json:"envID"`

	LogsCollectorAddress string `json:"address"`
	AccessTokenHash      string `json:"accessTokenHash,omitempty"`
}

// HashAccessToken computes the SHA-512 hash of an access token.
func HashAccessToken(token string) string {
	h := sha512.Sum512([]byte(token))

	return hex.EncodeToString(h[:])
}
