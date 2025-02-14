package auth

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const (
	keySuffixLength = 4

	keyLength = 20
)

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
		HashedValue:      HashKey(keyBytes),
		MaskedValue:      MaskKey(prefix, generatedToken),
	}, nil
}
