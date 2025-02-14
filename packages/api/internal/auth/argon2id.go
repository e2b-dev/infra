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

type argon2IDHashing struct {
	Params Argon2IDParams
}

func NewArgon2IDHashing() *argon2IDHashing {
	return &argon2IDHashing{
		Params: defaultArgon2IDParams,
	}
}

func generateSalt() []byte {
	// No salt used for keys, so we can query DB directly
	return []byte("e2b")
}

func (h *argon2IDHashing) Hash(key []byte) string {
	salt := generateSalt()

	hashBytes := argon2.IDKey(
		key,
		salt,
		h.Params.Time,
		h.Params.Memory,
		h.Params.Parallelism,
		h.Params.KeyLength,
	)

	salt64 := base64.RawStdEncoding.EncodeToString(salt)
	hash64 := base64.RawStdEncoding.EncodeToString(hashBytes)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d,l=%d$%s$%s",
		argon2.Version,
		h.Params.Memory,
		h.Params.Time,
		h.Params.Parallelism,
		h.Params.KeyLength,
		salt64,
		hash64,
	)
}
