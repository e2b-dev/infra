package auth

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/argon2"
)

const accessTokenSalt = "e2b_access_token_hash_salt"

func MaskAccessToken(key string) string {
	prefixLength := len(AccessTokenPrefix)
	firstFour := key[:prefixLength]
	lastFour := key[len(key)-prefixLength:]
	stars := strings.Repeat("*", len(key)-8)
	return firstFour + stars + lastFour
}

func HashAccessToken(key string) string {
	hashBytes := argon2.IDKey([]byte(key), []byte(accessTokenSalt), 1, 64*1024, 4, 32)
	return hex.EncodeToString(hashBytes)
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
