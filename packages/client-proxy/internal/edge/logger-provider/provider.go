package logger_provider

import (
	"context"
	"time"
)

type LogsQueryProvider interface {
	QueryBuildLogs(ctx context.Context, templateId string, buildId string, start time.Time, end time.Time, limit int, offset int) ([]LogEntry, error)
	QuerySandboxLogs(ctx context.Context, teamId string, sandboxId string, start time.Time, end time.Time, limit int, offset int) ([]LogEntry, error)
}

type LogEntry struct {
	Timestamp time.Time
	Line      string
}

func GetLogsQueryProvider() (LogsQueryProvider, error) {
	return NewLokiQueryProvider()
}
