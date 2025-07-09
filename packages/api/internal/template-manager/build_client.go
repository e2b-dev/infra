package template_manager

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	buildlogs "github.com/e2b-dev/infra/packages/api/internal/template-manager/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type BuildClient struct {
	GRPC *edge.ClusterGRPC

	logProviders []buildlogs.Provider
}

func (bc *BuildClient) GetLogs(ctx context.Context, templateID, buildID string, offset *int32) []string {
	logsTotal := make([]string, 0)
	for _, provider := range bc.logProviders {
		logs, err := provider.GetLogs(ctx, templateID, buildID, offset)
		if err != nil {
			telemetry.ReportEvent(ctx, "soft error when getting logs for template build", telemetry.WithTemplateID(templateID), telemetry.WithBuildID(buildID), attribute.String("provider", fmt.Sprintf("%T", provider)))
			continue
		}

		// Return the first non-empty logs, the providers are ordered by most up-to-date data
		if len(logs) > 0 {
			logsTotal = logs
			break
		}
	}

	return logsTotal
}
