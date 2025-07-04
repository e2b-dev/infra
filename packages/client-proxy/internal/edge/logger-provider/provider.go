package logger_provider

import (
	"context"
	"time"
)

type LogsQueryProvider interface {
	QueryBuildLogs(ctx context.Context, templateID string, buildID string, start time.Time, end time.Time, limit int, offset int) ([]LogEntry, error)
	QuerySandboxLogs(ctx context.Context, teamID string, sandboxID string, start time.Time, end time.Time, limit int, offset int) ([]LogEntry, error)
}

type LogEntry struct {
	Timestamp time.Time
	Line      string
}

func GetLogsQueryProvider() (LogsQueryProvider, error) {
	return NewLokiQueryProvider()
}
