package sandbox

import (
	"errors"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

const sandboxTrafficPrefix = "sandbox-traffic"

type AccessTokenGenerator struct {
	hasher *keys.HMACSha256Hashing
}

func NewAccessTokenGenerator(seedKey string) (*AccessTokenGenerator, error) {
	if seedKey == "" {
		return nil, errors.New("seed key is not set")
	}

	return &AccessTokenGenerator{
		hasher: keys.NewHMACSHA256Hashing([]byte(seedKey)),
	}, nil
}

func (g *AccessTokenGenerator) GenerateEnvdAccessToken(id api.SandboxID) (string, error) {
	return g.hasher.Hash([]byte(id))
}

func (g *AccessTokenGenerator) GenerateTrafficAccessToken(id api.SandboxID) (string, error) {
	key := fmt.Sprintf("%s-%s", sandboxTrafficPrefix, id)

	return g.hasher.Hash([]byte(key))
}
