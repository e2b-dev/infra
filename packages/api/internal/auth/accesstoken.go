package auth

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func MaskAccessToken(key string) string {
	prefixLength := len(AccessTokenPrefix)
	firstFour := key[:prefixLength]
	lastFour := key[len(key)-prefixLength:]
	stars := strings.Repeat("*", len(key)-8)
	return firstFour + stars + lastFour
}

func GenerateAccessToken() string {
	randomBytes := make([]byte, 20)
	_, err := rand.Read(randomBytes)
	if err != nil {
		panic(err) // Handle error appropriately
	}

	// Encode the random bytes to a hexadecimal string
	generatedToken := hex.EncodeToString(randomBytes)

	return AccessTokenPrefix + generatedToken
}
