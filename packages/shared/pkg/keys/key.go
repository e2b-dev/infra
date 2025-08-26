package keys

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	identifierValueSuffixLength = 4
	identifierValuePrefixLength = 2

	keyLength = 20
)

var hasher Hasher = NewSHA256Hashing()

type Key struct {
	PrefixedRawValue string
	HashedValue      string
	Masked           MaskedIdentifier
}

type MaskedIdentifier struct {
	Prefix            string
	ValueLength       int
	MaskedValuePrefix string
	MaskedValueSuffix string
}

// MaskKey returns identifier masking properties in accordance to the OpenAPI response spec
func MaskKey(prefix, value string) (MaskedIdentifier, error) {
	valueLength := len(value)

	suffixOffset := valueLength - identifierValueSuffixLength
	prefixOffset := identifierValuePrefixLength

	if suffixOffset < 0 {
		return MaskedIdentifier{}, fmt.Errorf("mask value length is less than identifier suffix length (%d)", identifierValueSuffixLength)
	}

	if suffixOffset == 0 {
		return MaskedIdentifier{}, fmt.Errorf("mask value length is equal to identifier suffix length (%d), which would expose the entire identifier in the mask", identifierValueSuffixLength)
	}

	// cap prefixOffset by suffixOffset to prevent overlap with the suffix.
	if prefixOffset > suffixOffset {
		prefixOffset = suffixOffset
	}

	maskPrefix := value[:prefixOffset]
	maskSuffix := value[suffixOffset:]

	maskedIdentifierProperties := MaskedIdentifier{
		Prefix:            prefix,
		ValueLength:       valueLength,
		MaskedValuePrefix: maskPrefix,
		MaskedValueSuffix: maskSuffix,
	}

	return maskedIdentifierProperties, nil
}

func GenerateKey(prefix string) (Key, error) {
	keyBytes := make([]byte, keyLength)

	_, err := rand.Read(keyBytes)
	if err != nil {
		return Key{}, err
	}

	generatedIdentifier := hex.EncodeToString(keyBytes)

	mask, err := MaskKey(prefix, generatedIdentifier)
	if err != nil {
		return Key{}, err
	}

	return Key{
		PrefixedRawValue: prefix + generatedIdentifier,
		HashedValue:      hasher.Hash(keyBytes),
		Masked:           mask,
	}, nil
}

func VerifyKey(prefix string, key string) (string, error) {
	if !strings.HasPrefix(key, prefix) {
		return "", fmt.Errorf("invalid key prefix")
	}

	keyValue := key[len(prefix):]
	keyBytes, err := hex.DecodeString(keyValue)
	if err != nil {
		return "", fmt.Errorf("invalid key")
	}

	return hasher.Hash(keyBytes), nil
}
