package logs

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

type Provider interface {
	GetLogs(ctx context.Context, templateID string, buildID string, offset int32, level *logs.LogLevel) ([]logs.LogEntry, error)
}
