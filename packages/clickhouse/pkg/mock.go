package clickhouse

import (
	"context"
	"time"
)

type NoopClient struct{}

func NewNoopClient() Clickhouse {
	return &NoopClient{}
}

func (m *NoopClient) Close(ctx context.Context) error {
	return nil
}

func (m *NoopClient) QueryLatestMetrics(ctx context.Context, sandboxIDs []string, teamID string) ([]Metrics, error) {
	return nil, nil
}

func (m *NoopClient) QuerySandboxTimeRange(ctx context.Context, sandboxID string, teamID string) (time.Time, time.Time, error) {
	return time.Time{}, time.Now(), nil
}

func (m *NoopClient) QuerySandboxMetrics(ctx context.Context, sandboxID string, teamID string, start time.Time, end time.Time, step time.Duration) ([]Metrics, error) {
	return nil, nil
}

func (m *NoopClient) QuerySandboxEvents(ctx context.Context, sandboxID string, limit, offset int) ([]SandboxEvent, error) {
	return nil, nil
}

func (m *NoopClient) InsertSandboxEvent(ctx context.Context, event SandboxEvent) error {
	return nil
}
