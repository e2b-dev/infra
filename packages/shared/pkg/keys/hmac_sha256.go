package keys

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

type HMACSha256Hashing struct {
	key []byte
}

func NewHMACSHA256Hashing(key []byte) *HMACSha256Hashing {
	return &HMACSha256Hashing{key: key}
}

func (h *HMACSha256Hashing) Hash(content []byte) string {
	mac := hmac.New(sha256.New, h.key)
	mac.Write(content)
	return hex.EncodeToString(mac.Sum(nil))
}
