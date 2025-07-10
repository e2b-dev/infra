package clickhouse

import (
	"context"
)

type NoopClient struct{}

func NewNoopClient() *NoopClient {
	return &NoopClient{}
}

func (m *NoopClient) Close(ctx context.Context) error {
	return nil
}

func (m *NoopClient) QueryLatestMetrics(ctx context.Context, sandboxIDs []string, teamID string) ([]Metrics, error) {
	return nil, nil
}
