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
	MaskedValue      string
}

type MaskedIdentifier struct {
	Prefix            string
	ValueLength       int
	MaskedValuePrefix string
	MaskedValueSuffix string
}

// GetMaskedIdentifierProperties returns identifier masking properties in accordance to the OpenAPI response spec
// NOTE: This is a temporary function which should eventually replace [MaskKey]/[GenerateKey] when db migration is completed
func GetMaskedIdentifierProperties(prefix, identifier string) (MaskedIdentifier, error) {
	identifierValue := strings.TrimPrefix(identifier, prefix)
	identifierValueLength := len(identifierValue)

	suffixOffset := identifierValueLength - identifierValueSuffixLength
	prefixOffset := identifierValueLength - identifierValuePrefixLength

	if suffixOffset < 0 {
		return MaskedIdentifier{}, fmt.Errorf("mask value length is less than key suffix length (%d)", identifierValueSuffixLength)
	}

	maskPrefix := identifierValue[:prefixOffset]
	maskSuffix := identifierValue[suffixOffset:]

	maskedIdentifierProperties := MaskedIdentifier{
		Prefix:            prefix,
		ValueLength:       identifierValueLength,
		MaskedValuePrefix: maskPrefix,
		MaskedValueSuffix: maskSuffix,
	}

	return maskedIdentifierProperties, nil
}

func MaskKey(prefix string, value string) (string, error) {
	suffixOffset := len(value) - identifierValueSuffixLength

	if suffixOffset < 0 {
		return "", fmt.Errorf("mask value length is less than key suffix length (%d)", identifierValueSuffixLength)
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

	generatedIdentifier := hex.EncodeToString(keyBytes)

	mask, err := MaskKey(prefix, generatedIdentifier)
	if err != nil {
		return Key{}, err
	}

	return Key{
		PrefixedRawValue: prefix + generatedIdentifier,
		HashedValue:      hasher.Hash(keyBytes),
		MaskedValue:      mask,
	}, nil
}
