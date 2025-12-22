package template_manager

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	buildlogs "github.com/e2b-dev/infra/packages/api/internal/template-manager/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	maxTimeRangeDuration = 7 * 24 * time.Hour
)

func GetBuildLogs(ctx context.Context, cluster *clusters.Cluster, nodeID *string, templateID, buildID string, offset int32, limit int32, level *logs.LogLevel, cursor *time.Time, direction api.LogsDirection, source *api.LogsSource) []logs.LogEntry {
	ctx, span := tracer.Start(ctx, "get build-logs")
	defer span.End()
	l := logger.L().With(logger.WithTemplateID(templateID), logger.WithBuildID(buildID))

	logProviders := make([]buildlogs.Provider, 0)

	if nodeID != nil && isSourceType(source, api.LogsSourceTemporary) {
		instance, err := cluster.GetTemplateBuilderByNodeID(*nodeID)
		if err == nil {
			grpc := cluster.GetGRPC(instance.ServiceInstanceID)
			logProviders = append(logProviders, &buildlogs.TemplateManagerProvider{GRPC: grpc})
		} else {
			l.Debug(ctx, "falling back to edge provider, node not found", zap.Error(err), logger.WithNodeID(*nodeID))
		}
	}

	if isSourceType(source, api.LogsSourcePersistent) {
		logProviders = append(logProviders, &buildlogs.EdgeProvider{HTTP: cluster.GetHTTP()})
	}

	start, end := time.Now().Add(-maxTimeRangeDuration), time.Now()
	if cursor != nil {
		if direction == api.LogsDirectionForward {
			start = *cursor
			end = start.Add(maxTimeRangeDuration)
		} else {
			end = *cursor
			start = end.Add(-maxTimeRangeDuration)
		}
	}

	for _, provider := range logProviders {
		l, err := provider.GetLogs(ctx, templateID, buildID, offset, limit, level, start, end, direction)
		if err != nil {
			telemetry.ReportError(ctx, "soft error when getting logs for template build", err, telemetry.WithTemplateID(templateID), telemetry.WithBuildID(buildID), attribute.String("provider", fmt.Sprintf("%T", provider)))

			continue
		}

		span.SetStatus(codes.Ok, "logs fetched for template build")
		// Return the first non-error logs, the providers are ordered by most up-to-date data
		return l
	}

	return nil
}

func isSourceType(source *api.LogsSource, sourceType api.LogsSource) bool {
	return source == nil || *source == sourceType
}
