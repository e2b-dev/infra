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
	suffixLength := 4

	firstFour := key[:prefixLength]
	lastFour := key[len(key)-suffixLength:]
	stars := strings.Repeat("*", len(key)-prefixLength-suffixLength)
	return firstFour + stars + lastFour
}

func HashAccessToken(key string) string {
	hashBytes := argon2.IDKey([]byte(key), []byte(accessTokenSalt), 1, 64*1024, 4, 32)
	return hex.EncodeToString(hashBytes)
}

func GenerateAccessToken() (string, error) {
	randomBytes := make([]byte, 20)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", err
	}

	// Encode the random bytes to a hexadecimal string
	generatedToken := hex.EncodeToString(randomBytes)

	return AccessTokenPrefix + generatedToken, nil
}
