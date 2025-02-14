package auth

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/argon2"
)

const apiKeySalt = "e2b_api_key_hash_salt"

func MaskAPIKey(key string) string {
	prefixLength := len(ApiKeyPrefix)
	suffixLength := 4

	firstFour := key[:prefixLength]
	lastFour := key[len(key)-suffixLength:]
	stars := strings.Repeat("*", len(key)-prefixLength-suffixLength)
	return firstFour + stars + lastFour
}

func HashAPIKey(key string) string {
	hashBytes := argon2.IDKey([]byte(key), []byte(apiKeySalt), 1, 64*1024, 4, 32)
	return hex.EncodeToString(hashBytes)
}

func GenerateTeamAPIKey() (string, error) {
	randomBytes := make([]byte, 20)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", err
	}

	// Encode the random bytes to a hexadecimal string
	generatedToken := hex.EncodeToString(randomBytes)

	return ApiKeyPrefix + generatedToken, nil
}
