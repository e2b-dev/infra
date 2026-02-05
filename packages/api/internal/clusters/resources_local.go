package clusters

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/grafana/loki/pkg/logproto"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	clickhouseutils "github.com/e2b-dev/infra/packages/clickhouse/pkg/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type LocalClusterResourceProvider struct {
	config                      cfg.Config
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
	config cfg.Config,
) ClusterResource {
	return &LocalClusterResourceProvider{
		config:                      config,
		querySandboxMetricsProvider: querySandboxMetricsProvider,
		queryLogsProvider:           queryLogsProvider,
		instances:                   instances,
	}
}

func (l *LocalClusterResourceProvider) GetVolumeTypes(_ context.Context) ([]string, error) {
	var volumeTypes []string

	for volumeType := range l.config.PersistentVolumeMounts {
		volumeTypes = append(volumeTypes, volumeType)
	}

	return volumeTypes, nil
}

func (l *LocalClusterResourceProvider) GetSandboxMetrics(ctx context.Context, teamID string, sandboxID string, qStart *int64, qEnd *int64) ([]api.SandboxMetric, *api.APIError) {
	start, end, err := clickhouseutils.GetSandboxStartEndTime(ctx, l.querySandboxMetricsProvider, teamID, sandboxID, qStart, qEnd)
	if err != nil {
		return nil, &api.APIError{
			Err:       fmt.Errorf(`error when getting metrics time range: %w`, err),
			ClientMsg: "Failed to fetch sandbox metrics",
			Code:      http.StatusInternalServerError,
		}
	}

	start, end, err = clickhouseutils.ValidateRange(start, end)
	if err != nil {
		return nil, &api.APIError{
			Err:       fmt.Errorf(`error when validating range of metrics: %w`, err),
			ClientMsg: "Invalid time range for metrics",
			Code:      http.StatusBadRequest,
		}
	}

	// Calculate the step size
	step := clickhouseutils.CalculateStep(start, end)

	rawMetrics, err := l.querySandboxMetricsProvider.QuerySandboxMetrics(ctx, sandboxID, teamID, start, end, step)
	if err != nil {
		return nil, &api.APIError{
			Err:       fmt.Errorf(`error when querying sandbox metrics: %w`, err),
			ClientMsg: "Failed to fetch sandbox metrics",
			Code:      http.StatusInternalServerError,
		}
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

func (l *LocalClusterResourceProvider) GetSandboxesMetrics(ctx context.Context, teamID string, sandboxIDs []string) (map[string]api.SandboxMetric, *api.APIError) {
	rawMetrics, err := l.querySandboxMetricsProvider.QueryLatestMetrics(ctx, sandboxIDs, teamID)
	if err != nil {
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: "Failed to fetch sandbox metrics",
			Code:      http.StatusInternalServerError,
		}
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

func (l *LocalClusterResourceProvider) GetSandboxLogs(ctx context.Context, teamID string, sandboxID string, qStart *int64, qLimit *int32) (api.SandboxLogs, *api.APIError) {
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
		return api.SandboxLogs{}, &api.APIError{
			Err:       fmt.Errorf("error when fetching sandbox logs: %w", err),
			ClientMsg: "Failed to fetch sandbox logs",
			Code:      http.StatusInternalServerError,
		}
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

func (l *LocalClusterResourceProvider) GetBuildLogs(
	ctx context.Context,
	nodeID *string,
	templateID string,
	buildID string,
	offset int32,
	limit int32,
	level *logs.LogLevel,
	cursor *time.Time,
	direction api.LogsDirection,
	source *api.LogsSource,
) ([]logs.LogEntry, *api.APIError) {
	// Use shared implementation with Loki as the persistent log backend
	start, end := logQueryWindow(cursor, direction)

	lokiDirection := defaultDirection
	if direction == api.LogsDirectionBackward {
		lokiDirection = logproto.BACKWARD
	}

	persistentFetcher := l.logsFromLocalLoki(ctx, templateID, buildID, start, end, int(limit), offset, level, lokiDirection)

	return getBuildLogsWithSources(ctx, l.instances, nodeID, templateID, buildID, offset, limit, level, cursor, direction, source, persistentFetcher)
}

func (l *LocalClusterResourceProvider) logsFromLocalLoki(ctx context.Context, templateID string, buildID string, start time.Time, end time.Time, limit int, offset int32, level *logs.LogLevel, direction logproto.Direction) logSourceFunc {
	return func() ([]logs.LogEntry, *api.APIError) {
		entries, err := l.queryLogsProvider.QueryBuildLogs(ctx, templateID, buildID, start, end, limit, offset, level, direction)
		if err != nil {
			return nil, &api.APIError{
				Err:       fmt.Errorf("error when fetching build logs from Loki: %w", err),
				ClientMsg: "Failed to fetch build logs",
				Code:      http.StatusInternalServerError,
			}
		}

		return entries, nil
	}
}
