package logger_provider

import (
	"context"
	"time"

	"github.com/grafana/loki/pkg/logproto"

	"github.com/e2b-dev/infra/packages/proxy/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

type LogsQueryProvider interface {
	QueryBuildLogs(ctx context.Context, templateID string, buildID string, start time.Time, end time.Time, limit int, offset int32, level *logs.LogLevel, direction logproto.Direction) ([]logs.LogEntry, error)
	QuerySandboxLogs(ctx context.Context, teamID string, sandboxID string, start time.Time, end time.Time, limit int) ([]logs.LogEntry, error)
}

func GetLogsQueryProvider(config cfg.Config) (LogsQueryProvider, error) {
	return NewLokiQueryProvider(config)
}
