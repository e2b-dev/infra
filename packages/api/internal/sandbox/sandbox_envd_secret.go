package sandbox

import (
	"errors"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"os"
)

var (
	saltKey = os.Getenv("SANDBOX_ACCESS_TOKEN_HASH_SEED")
)

type EnvdAccessTokenGenerator struct {
}

func NewEnvdAccessTokenGenerator() *EnvdAccessTokenGenerator {
	return &EnvdAccessTokenGenerator{}
}

func (g *EnvdAccessTokenGenerator) GenerateAccessToken(id api.SandboxID) (string, error) {
	if saltKey == "" {
		return "", errors.New("salt key is not set")
	}

	hasher := keys.NewHMACSHA256Hashing([]byte(saltKey))
	return hasher.Hash([]byte(id))
}
