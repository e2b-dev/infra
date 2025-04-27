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

func (h *HMACSha256Hashing) Hash(content []byte) (string, error) {
	mac := hmac.New(sha256.New, h.key)
	_, err := mac.Write(content)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(mac.Sum(nil)), nil
}
