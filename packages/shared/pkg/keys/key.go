package keys

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	tokenValueSuffixLength = 4
	tokenValuePrefixLength = 2

	keyLength = 20
)

var hasher Hasher = NewSHA256Hashing()

type Key struct {
	PrefixedRawValue string
	HashedValue      string
	MaskedValue      string
}

type MaskedToken struct {
	TokenPrefix       string
	ValueLength       int
	MaskedValuePrefix string
	MaskedValueSuffix string
}

// GetMaskedTokenProperties returns token masking properties in accordance to the OpenAPI response spec
// NOTE: This is a temporary function which should eventually replace [MaskKey] when db migration is completed
func GetMaskedTokenProperties(prefix, token string) (MaskedToken, error) {
	tokenValue := strings.TrimPrefix(token, prefix)
	tokenValueLength := len(tokenValue)

	suffixOffset := tokenValueLength - tokenValueSuffixLength
	prefixOffset := tokenValueLength - tokenValuePrefixLength

	if suffixOffset < 0 {
		return MaskedToken{}, fmt.Errorf("mask value length is less than key suffix length (%d)", tokenValueSuffixLength)
	}

	maskPrefix := tokenValue[:prefixOffset]
	maskSuffix := tokenValue[suffixOffset:]

	maskedTokenProperties := MaskedToken{
		TokenPrefix:       prefix,
		ValueLength:       tokenValueLength,
		MaskedValuePrefix: maskPrefix,
		MaskedValueSuffix: maskSuffix,
	}

	return maskedTokenProperties, nil
}

func MaskKey(prefix string, value string) (string, error) {
	suffixOffset := len(value) - tokenValueSuffixLength

	if suffixOffset < 0 {
		return "", fmt.Errorf("mask value length is less than key suffix length (%d)", tokenValueSuffixLength)
	}

	lastFour := value[suffixOffset:]
	stars := strings.Repeat("*", suffixOffset)
	return prefix + stars + lastFour, nil
}

func GenerateKey(prefix string) (Key, error) {
	keyBytes := make([]byte, keyLength)

	_, err := rand.Read(keyBytes)
	if err != nil {
		return Key{}, err
	}

	generatedToken := hex.EncodeToString(keyBytes)

	mask, err := MaskKey(prefix, generatedToken)
	if err != nil {
		return Key{}, err
	}

	return Key{
		PrefixedRawValue: prefix + generatedToken,
		HashedValue:      hasher.Hash(keyBytes),
		MaskedValue:      mask,
	}, nil
}
