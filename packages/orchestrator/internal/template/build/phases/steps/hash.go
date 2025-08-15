package steps

import (
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (sb *StepBuilder) Hash(sourceLayer phases.LayerResult) (string, error) {
	return cache.HashKeys(
		sourceLayer.Hash,
		sb.step.Type,
		strings.Join(sb.step.Args, " "),
		utils.Sprintp(sb.step.FilesHash),
	), nil
}
