package keys

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	keySuffixLength = 4
	keyPrefixLength = 2

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
func GetMaskedTokenProperties(prefix, value string) (MaskedToken, error) {
	suffixOffset := len(value) - keySuffixLength
	prefixOffset := keyPrefixLength

	if suffixOffset < 0 {
		return MaskedToken{}, fmt.Errorf("mask value length is less than key suffix length (%d)", keySuffixLength)
	}

	maskPrefix := value[:prefixOffset]
	maskSuffix := value[suffixOffset:]
	tokenLength := len(value)

	maskedTokenProperties := MaskedToken{
		TokenPrefix:       prefix,
		ValueLength:       tokenLength,
		MaskedValuePrefix: maskPrefix,
		MaskedValueSuffix: maskSuffix,
	}

	return maskedTokenProperties, nil
}

func MaskKey(prefix string, value string) (string, error) {
	suffixOffset := len(value) - keySuffixLength

	if suffixOffset < 0 {
		return "", fmt.Errorf("mask value length is less than key suffix length (%d)", keySuffixLength)
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
