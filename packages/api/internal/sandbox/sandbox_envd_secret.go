package sandbox

import (
	"errors"
	"fmt"
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

	raw := []byte(fmt.Sprintf("%s%s", id, saltKey))
	hasher := keys.NewSHA256Hashing()

	return hasher.HashWithoutPrefix(raw), nil
}
