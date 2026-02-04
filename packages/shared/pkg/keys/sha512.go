package keys

import (
	"crypto/sha512"
	"encoding/hex"
)

// HashAccessToken computes the SHA-512 hash of an access token.
func HashAccessToken(token string) string {
	h := sha512.Sum512([]byte(token))

	return hex.EncodeToString(h[:])
}
