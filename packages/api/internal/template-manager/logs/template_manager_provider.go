package logs

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateManagerProvider struct {
	GRPC *edge.ClusterGRPC
}

func (t *TemplateManagerProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset *int32, level *api.LogLevel) ([]api.BuildLogEntry, error) {
	reqCtx := metadata.NewOutgoingContext(ctx, t.GRPC.Metadata)
	res, err := t.GRPC.Client.Template.TemplateBuildStatus(
		reqCtx, &templatemanagergrpc.TemplateStatusRequest{
			TemplateID: templateID,
			BuildID:    buildID,
			Offset:     offset,
			Level:      templatemanagergrpc.LogLevel(levelToNumber(level)).Enum(),
		},
	)
	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))
		return nil, err
	}

	logs := make([]api.BuildLogEntry, 0)
	// Add an extra newline to each log entry to ensure proper formatting in the CLI
	for _, entry := range res.GetLogEntries() {
		logs = append(logs, api.BuildLogEntry{
			Timestamp: entry.GetTimestamp().AsTime(),
			Message:   entry.GetMessage(),
			Level:     numberToLevel(LogLevel(entry.GetLevel())),
		})
	}

	return logs, nil
}
