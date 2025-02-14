package auth

import (
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/argon2"
)

type Argon2IDParams struct {
	Memory      uint32
	Time        uint32
	Parallelism uint8
	KeyLength   uint32
}

var (
	defaultArgon2IDParams = Argon2IDParams{
		Memory:      64 * 1024,
		Time:        1,
		Parallelism: 4,
		KeyLength:   32,
	}
)

func generateSalt() []byte {
	// No salt used for keys, so we can query DB directly
	return []byte("e2b")
}

func HashKey(key string) string {
	return HashKeyWithParams(key, &defaultArgon2IDParams)
}

func HashKeyWithParams(key string, params *Argon2IDParams) string {
	salt := generateSalt()

	hashBytes := argon2.IDKey(
		[]byte(key),
		[]byte(salt),
		params.Time,
		params.Memory,
		params.Parallelism,
		params.KeyLength,
	)

	salt64 := base64.RawStdEncoding.EncodeToString(salt)
	hash64 := base64.RawStdEncoding.EncodeToString(hashBytes)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d,l=%d$%s$%s",
		argon2.Version,
		params.Memory,
		params.Time,
		params.Parallelism,
		params.KeyLength,
		salt64,
		hash64,
	)
}
