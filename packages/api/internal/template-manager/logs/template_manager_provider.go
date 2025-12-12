package logs

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/edge"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type TemplateManagerProvider struct {
	GRPC *edge.ClusterGRPC
}

func (t *TemplateManagerProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset int32, limit int32, level *logs.LogLevel, start time.Time, end time.Time, direction api.LogsDirection) ([]logs.LogEntry, error) {
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
			Limit:      utils.ToPtr(uint32(limit)),
			Level:      lvlReq,
			Start:      timestamppb.New(start),
			End:        timestamppb.New(end),
			Direction:  utils.ToPtr(logDirectionToTemplateManagerDirection(direction)),
		},
	)
	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		logger.L().Error(ctx, "error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))

		return nil, err
	}

	l := make([]logs.LogEntry, 0)
	// Add an extra newline to each log entry to ensure proper formatting in the CLI
	for _, entry := range res.GetLogEntries() {
		l = append(l, logs.LogEntry{
			Timestamp: entry.GetTimestamp().AsTime(),
			Message:   entry.GetMessage(),
			Level:     logs.LogLevel(entry.GetLevel()),
			Fields:    entry.GetFields(),
		})
	}

	return l, nil
}

func logDirectionToTemplateManagerDirection(direction api.LogsDirection) templatemanagergrpc.LogsDirection {
	switch direction {
	case api.LogsDirectionForward:
		return templatemanagergrpc.LogsDirection_Forward
	case api.LogsDirectionBackward:
		return templatemanagergrpc.LogsDirection_Backward
	default:
		return templatemanagergrpc.LogsDirection_Forward
	}
}
