package user

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
)

const prefix = "DEFAULT USER"

func (ub *UserBuilder) Hash(sourceLayer phases.LayerResult) (string, error) {
	return cache.HashKeys(
		sourceLayer.Hash,
		prefix,
		ub.user,
	), nil
}
