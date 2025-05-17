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

type MaskedResponseKey struct {
	KeyPrefix  string
	KeyLength  int
	MaskPrefix string
	MaskSuffix string
}

func MaskResponseKey(prefix, value string) (MaskedResponseKey, error) {
	suffixOffset := len(value) - keySuffixLength
	prefixOffset := keyPrefixLength

	if suffixOffset < 0 {
		return MaskedResponseKey{}, fmt.Errorf("mask value length is less than key suffix length (%d)", keySuffixLength)
	}

	maskPrefix := value[:prefixOffset]
	maskSuffix := value[suffixOffset:]
	keyLength := len(value)

	maskedKeyResponseProperties := MaskedResponseKey{
		KeyPrefix:  prefix,
		KeyLength:  keyLength,
		MaskPrefix: maskPrefix,
		MaskSuffix: maskSuffix,
	}

	return maskedKeyResponseProperties, nil
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
