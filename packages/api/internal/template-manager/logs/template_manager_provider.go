package logs

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/edge"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateManagerProvider struct {
	GRPC *edge.ClusterGRPC
}

func (t *TemplateManagerProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset int32, level *logs.LogLevel) ([]logs.LogEntry, error) {
	reqCtx := metadata.NewOutgoingContext(ctx, t.GRPC.Metadata)

	var lvlReq *templatemanagergrpc.LogLevel
	if level != nil {
		lvlReq = templatemanagergrpc.LogLevel(*level).Enum()
	}
	res, err := t.GRPC.Client.Template.TemplateBuildStatus(
		reqCtx, &templatemanagergrpc.TemplateStatusRequest{
			TemplateID: templateID,
			BuildID:    buildID,
			Offset:     &offset,
			Level:      lvlReq,
		},
	)
	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))
		return nil, err
	}

	l := make([]logs.LogEntry, 0)
	// Add an extra newline to each log entry to ensure proper formatting in the CLI
	for _, entry := range res.GetLogEntries() {
		l = append(l, logs.LogEntry{
			Timestamp: entry.GetTimestamp().AsTime(),
			Message:   entry.GetMessage(),
			Level:     logs.LogLevel(entry.GetLevel()),
		})
	}

	return l, nil
}
