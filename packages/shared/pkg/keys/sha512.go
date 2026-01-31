package keys

import (
	"crypto/sha512"
	"encoding/hex"
)

type Sha512Hashing struct{}

func NewSHA512Hashing() *Sha512Hashing {
	return &Sha512Hashing{}
}

func (h *Sha512Hashing) Hash(key []byte) string {
	hashBytes := sha512.Sum512(key)

	return hex.EncodeToString(hashBytes[:])
}

// HashAccessToken computes the SHA-512 hash of an access token.
// This is used for secure token validation via MMDS in Firecracker VMs.
func HashAccessToken(token string) string {
	h := sha512.Sum512([]byte(token))

	return hex.EncodeToString(h[:])
}
