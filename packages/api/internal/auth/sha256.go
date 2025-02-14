package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

type sha256Hashing struct {
}

func NewSHA256Hashing() *sha256Hashing {
	return &sha256Hashing{}
}

func (h *sha256Hashing) Hash(key []byte) string {
	hashBytes := sha256.Sum256(key)

	hash64 := base64.RawStdEncoding.EncodeToString(hashBytes[:])

	return fmt.Sprintf(
		"$sha256$%s",
		hash64,
	)
}
