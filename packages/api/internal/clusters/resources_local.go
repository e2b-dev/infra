package clusters

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	clickhouseutils "github.com/e2b-dev/infra/packages/clickhouse/pkg/utils"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type LocalClusterResourceProvider struct {
	querySandboxMetricsProvider clickhouse.SandboxQueriesProvider
	queryLogsProvider           *loki.LokiQueryProvider
	instances                   *smap.Map[*Instance]
}

const (
	sandboxLogsOldestLimit = 168 * time.Hour // 7 days
	defaultLogsLimit       = 1000
	defaultDirection       = logproto.FORWARD
)

func newLocalClusterResourceProvider(
	querySandboxMetricsProvider clickhouse.SandboxQueriesProvider,
	queryLogsProvider *loki.LokiQueryProvider,
	instances *smap.Map[*Instance],
) ClusterResource {
	return &LocalClusterResourceProvider{
		querySandboxMetricsProvider: querySandboxMetricsProvider,
		queryLogsProvider:           queryLogsProvider,
		instances:                   instances,
	}
}

func (l *LocalClusterResourceProvider) GetSandboxMetrics(ctx context.Context, teamID string, sandboxID string, qStart *int64, qEnd *int64) ([]api.SandboxMetric, error) {
	start, end, err := clickhouseutils.GetSandboxStartEndTime(ctx, l.querySandboxMetricsProvider, teamID, sandboxID, qStart, qEnd)
	if err != nil {
		return nil, fmt.Errorf(`error when getting metrics time range: %w`, err)
	}

	start, end, err = clickhouseutils.ValidateRange(start, end)
	if err != nil {
		return nil, fmt.Errorf(`error when validating range of metrics: %w`, err)
	}

	// Calculate the step size
	step := clickhouseutils.CalculateStep(start, end)

	rawMetrics, err := l.querySandboxMetricsProvider.QuerySandboxMetrics(ctx, sandboxID, teamID, start, end, step)
	if err != nil {
		return nil, fmt.Errorf(`error when querying sandbox metrics: %w`, err)
	}

	metrics := make([]api.SandboxMetric, len(rawMetrics))
	for i, m := range rawMetrics {
		metrics[i] = api.SandboxMetric{
			Timestamp:     m.Timestamp,
			TimestampUnix: m.Timestamp.Unix(),
			CpuUsedPct:    float32(m.CPUUsedPercent),
			CpuCount:      int32(m.CPUCount),
			MemTotal:      int64(m.MemTotal),
			MemUsed:       int64(m.MemUsed),
			DiskTotal:     int64(m.DiskTotal),
			DiskUsed:      int64(m.DiskUsed),
		}
	}

	return metrics, nil
}

func (l *LocalClusterResourceProvider) GetSandboxesMetrics(ctx context.Context, teamID string, sandboxIDs []string) (map[string]api.SandboxMetric, error) {
	rawMetrics, err := l.querySandboxMetricsProvider.QueryLatestMetrics(ctx, sandboxIDs, teamID)
	if err != nil {
		logger.L().Error(ctx, "Error fetching sandbox metrics from ClickHouse", logger.WithTeamID(teamID), zap.Error(err))

		return nil, err
	}

	metrics := make(map[string]api.SandboxMetric)
	for _, m := range rawMetrics {
		metrics[m.SandboxID] = api.SandboxMetric{
			Timestamp:     m.Timestamp,
			TimestampUnix: m.Timestamp.Unix(),
			CpuUsedPct:    float32(m.CPUUsedPercent),
			CpuCount:      int32(m.CPUCount),
			MemTotal:      int64(m.MemTotal),
			MemUsed:       int64(m.MemUsed),
			DiskTotal:     int64(m.DiskTotal),
			DiskUsed:      int64(m.DiskUsed),
		}
	}

	return metrics, nil
}

func (l *LocalClusterResourceProvider) GetSandboxLogs(ctx context.Context, teamID string, sandboxID string, qStart *int64, qLimit *int32) (api.SandboxLogs, error) {
	end := time.Now()
	var start time.Time

	if qStart != nil {
		start = time.UnixMilli(*qStart)
	} else {
		start = end.Add(-sandboxLogsOldestLimit)
	}

	limit := defaultLogsLimit
	if qLimit != nil {
		limit = int(*qLimit)
	}

	raw, err := l.queryLogsProvider.QuerySandboxLogs(ctx, teamID, sandboxID, start, end, limit)
	if err != nil {
		return api.SandboxLogs{}, fmt.Errorf("error when fetching sandbox logs: %w", err)
	}

	ll := make([]api.SandboxLog, len(raw))
	for i, row := range raw {
		ll[i] = api.SandboxLog{Line: row.Raw, Timestamp: row.Timestamp}
	}

	le := make([]api.SandboxLogEntry, len(raw))
	for i, row := range raw {
		le[i] = api.SandboxLogEntry{
			Timestamp: row.Timestamp,
			Level:     api.LogLevel(row.Level),
			Message:   row.Message,
			Fields:    row.Fields,
		}
	}

	return api.SandboxLogs{Logs: ll, LogEntries: le}, nil
}

func (l *LocalClusterResourceProvider) GetBuildLogs(ctx context.Context, nodeID *string, templateID string, buildID string, offset int32, limit int32, level *logs.LogLevel, cursor *time.Time, direction api.LogsDirection, source *api.LogsSource) ([]logs.LogEntry, error) {
	start, end := logQueryWindow(cursor, direction)

	lokiDirection := defaultDirection
	if direction == api.LogsDirectionBackward {
		lokiDirection = logproto.BACKWARD
	}

	// todo
	if source == nil {
		// try node and then default to Loki
	} else if *source == api.LogsSourcePersistent {
		// force to node
	} else if *source == api.LogsSourceTemporary {
		// force to loki
	}

	if nodeID != nil {
		instance, found := l.instances.Get(*nodeID)
		if found {
			var lvlReq *templatemanagergrpc.LogLevel
			if level != nil {
				lvlReq = templatemanagergrpc.LogLevel(*level).Enum()
			}

			res, err := instance.grpc.Template.TemplateBuildStatus(
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

			// Add an extra newline to each log entry to ensure proper formatting in the CLI
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

	logs, err := l.queryLogsProvider.QueryBuildLogs(ctx, templateID, buildID, start, end, int(limit), offset, level, lokiDirection)
	if err != nil {
		return nil, fmt.Errorf("error when fetching build logs: %w", err)
	}

	return logs, nil
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
