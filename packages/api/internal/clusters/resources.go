package clusters

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
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
