package keys

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

type Sha256Hashing struct {
}

func NewSHA256Hashing() *Sha256Hashing {
	return &Sha256Hashing{}
}

func (h *Sha256Hashing) Hash(key []byte) string {
	hashBytes := sha256.Sum256(key)

	hash64 := base64.RawStdEncoding.EncodeToString(hashBytes[:])

	return fmt.Sprintf(
		"$sha256$%s",
		hash64,
	)
}
