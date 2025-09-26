package sandbox

import (
	"errors"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

type EnvdAccessTokenGenerator struct {
	hasher *keys.HMACSha256Hashing
}

func NewEnvdAccessTokenGenerator(seedKey string) (*EnvdAccessTokenGenerator, error) {
	if seedKey == "" {
		return nil, errors.New("seed key is not set")
	}

	return &EnvdAccessTokenGenerator{
		hasher: keys.NewHMACSHA256Hashing([]byte(seedKey)),
	}, nil
}

func (g *EnvdAccessTokenGenerator) GenerateAccessToken(id api.SandboxID) (string, error) {
	return g.hasher.Hash([]byte(id))
}
