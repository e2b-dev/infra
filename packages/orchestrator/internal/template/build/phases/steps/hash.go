package steps

import (
	"context"
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (sb *StepBuilder) Hash(_ context.Context, sourceLayer phases.LayerResult) (string, error) {
	return cache.HashKeys(
		sourceLayer.Hash,
		sb.step.GetType(),
		strings.Join(sb.step.GetArgs(), " "),
		utils.Sprintp(sb.step.FilesHash), //nolint:protogetter // we need the nil check too
	), nil
}
