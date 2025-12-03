package template_manager

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	buildlogs "github.com/e2b-dev/infra/packages/api/internal/template-manager/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func GetBuildLogs(ctx context.Context, cluster *edge.Cluster, nodeID *string, templateID, buildID string, offset int32, level *logs.LogLevel) []logs.LogEntry {
	logProviders := make([]buildlogs.Provider, 0)

	if nodeID != nil {
		instance, err := cluster.GetTemplateBuilderByNodeID(*nodeID)
		if err == nil {
			grpc := cluster.GetGRPC(instance.ServiceInstanceID)

			logProviders = append(logProviders, &buildlogs.TemplateManagerProvider{GRPC: grpc})
		}
	}

	logProviders = append(logProviders, &buildlogs.EdgeProvider{HTTP: cluster.GetHTTP()})

	logsTotal := make([]logs.LogEntry, 0)
	for _, provider := range logProviders {
		l, err := provider.GetLogs(ctx, templateID, buildID, offset, level)
		if err != nil {
			telemetry.ReportEvent(ctx, "soft error when getting logs for template build", telemetry.WithTemplateID(templateID), telemetry.WithBuildID(buildID), attribute.String("provider", fmt.Sprintf("%T", provider)))

			continue
		}

		// Return the first non-error logs, the providers are ordered by most up-to-date data
		return l
	}

	return logsTotal
}
