package logs

import (
	"context"
)

type Provider interface {
	GetLogs(ctx context.Context, templateID string, buildID string, offset *int32) ([]string, error)
}
