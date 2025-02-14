package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	keySuffixLength = 4

	keyLength = 20
)

var hashing Hashing = NewSHA256Hashing()

type Key struct {
	PrefixedRawValue string
	HashedValue      string
	MaskedValue      string
}

func MaskKey(prefix string, value string) string {
	suffixLength := keySuffixLength

	lastFour := value[len(value)-suffixLength:]
	stars := strings.Repeat("*", len(value)-suffixLength)
	return prefix + stars + lastFour
}

func GenerateKey(prefix string) (Key, error) {
	keyBytes := make([]byte, keyLength)
	_, err := rand.Read(keyBytes)
	if err != nil {
		return Key{}, err
	}
	generatedToken := hex.EncodeToString(keyBytes)

	return Key{
		PrefixedRawValue: prefix + generatedToken,
		HashedValue:      hashing.Hash(keyBytes),
		MaskedValue:      MaskKey(prefix, generatedToken),
	}, nil
}

func VerifyKey(prefix string, key string) (string, error) {
	parts := strings.Split(key, prefix)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid key prefix")
	}

	keyValue := parts[1]
	keyBytes, err := hex.DecodeString(keyValue)
	if err != nil {
		return "", fmt.Errorf("invalid key")
	}

	return hashing.Hash(keyBytes), nil
}
