package buildlogs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateManagerProvider struct {
	TemplateManager *template_manager.TemplateManager
}

func (t *TemplateManagerProvider) GetLogs(ctx context.Context, templateID string, buildUUID uuid.UUID, offset int) ([]string, error) {
	logs := make([]string, 0)
	res, err := t.TemplateManager.GetBuildLogs(ctx, templateID, buildUUID)
	if err == nil {
		logsCrawled := 0

		for _, entry := range res {
			logsCrawled++

			// does not support offset pagination, so we need to skip logs manually
			if logsCrawled <= offset {
				continue
			}
			logs = append(logs, fmt.Sprintf("%s\n", entry))
		}
	} else {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildUUID.String()))
	}

	return logs, err
}
