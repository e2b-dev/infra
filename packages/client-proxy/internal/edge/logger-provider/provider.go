package logger_provider

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

type LogsQueryProvider interface {
	QueryBuildLogs(ctx context.Context, templateID string, buildID string, start time.Time, end time.Time, limit int, offset int32, level *logs.LogLevel) ([]logs.LogEntry, error)
	QuerySandboxLogs(ctx context.Context, teamID string, sandboxID string, start time.Time, end time.Time, limit int, offset int32) ([]logs.LogEntry, error)
}

func GetLogsQueryProvider() (LogsQueryProvider, error) {
	return NewLokiQueryProvider()
}
