package clusters

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type ClusterResource interface {
	GetSandboxMetrics(ctx context.Context, teamID string, sandboxID string, qStart *int64, qEnd *int64) ([]api.SandboxMetric, error)
	GetSandboxesMetrics(ctx context.Context, teamID string, sandboxIDs []string) (map[string]api.SandboxMetric, error)
	GetSandboxLogs(ctx context.Context, teamID string, sandboxID string, start *int64, limit *int32) (api.SandboxLogs, error)
	GetBuildLogs(ctx context.Context, nodeID *string, templateID string, buildID string, offset int32, limit int32, level *logs.LogLevel, cursor *time.Time, direction api.LogsDirection, source *api.LogsSource) ([]logs.LogEntry, error)
}

const (
	maxTimeRangeDuration = 7 * 24 * time.Hour
)

func logQueryWindow(cursor *time.Time, direction api.LogsDirection) (time.Time, time.Time) {
	start, end := time.Now().Add(-maxTimeRangeDuration), time.Now()
	if cursor == nil {
		return start, end
	}

	if direction == api.LogsDirectionForward {
		start = *cursor
		end = start.Add(maxTimeRangeDuration)
	} else {
		end = *cursor
		start = end.Add(-maxTimeRangeDuration)
	}

	return start, end
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

func logToEdgeLevel(level *logs.LogLevel) *edgeapi.LogLevel {
	if level == nil {
		return nil
	}

	value := edgeapi.LogLevel(logs.LevelToString(*level))

	return &value
}

func logCheckSourceType(source *api.LogsSource, sourceType api.LogsSource) bool {
	return source == nil || *source == sourceType
}

type logSourceFunc func() ([]logs.LogEntry, error)

func logsFromBuilderInstance(ctx context.Context, instance *Instance, templateID string, buildID string, offset int32, limit int32, level *logs.LogLevel, start time.Time, end time.Time, direction api.LogsDirection) logSourceFunc {
	return func() ([]logs.LogEntry, error) {
		var lvlReq *templatemanagergrpc.LogLevel
		if level != nil {
			lvlReq = templatemanagergrpc.LogLevel(*level).Enum()
		}

		res, err := instance.GetClient().Template.TemplateBuildStatus(
			ctx, &templatemanagergrpc.TemplateStatusRequest{
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

		raw := res.GetLogEntries()
		entries := make([]logs.LogEntry, len(raw))
		for i, entry := range raw {
			entries[i] = logs.LogEntry{
				Timestamp: entry.GetTimestamp().AsTime(),
				Message:   entry.GetMessage(),
				Level:     logs.LogLevel(entry.GetLevel()),
				Fields:    entry.GetFields(),
			}
		}

		return entries, nil
	}
}
