package steps

import (
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (sb *StepsBuilder) Hash(previousHash string, step *templatemanager.TemplateStep) string {
	return cache.HashKeys(
		previousHash,
		step.Type,
		strings.Join(step.Args, " "),
		utils.Sprintp(step.FilesHash),
	)
}
