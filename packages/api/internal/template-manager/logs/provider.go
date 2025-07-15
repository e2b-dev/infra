package logs

import (
	"context"
	"time"
)

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	Level     string    `json:"level"`
}

type Provider interface {
	GetLogs(ctx context.Context, templateID string, buildID string, offset *int32) ([]LogEntry, error)
}
