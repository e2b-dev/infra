package clickhouse

import (
	"context"
	"time"
)

type MockClient struct{}

func NewMockStore() *MockClient {
	return &MockClient{}
}

func (m *MockClient) Close(ctx context.Context) error {
	return nil
}

func (m *MockClient) InsertMetrics(ctx context.Context, metrics Metrics) error {
	return nil
}

func (m *MockClient) QueryMetrics(ctx context.Context, sandboxID, teamID string, start time.Time, limit int) ([]Metrics, error) {
	return nil, nil
}
