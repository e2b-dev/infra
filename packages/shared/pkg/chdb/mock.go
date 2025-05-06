package chdb

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/e2b-dev/infra/packages/shared/pkg/models/chmodels"
)

type MockStore struct{}

func NewMockStore() *MockStore {
	return &MockStore{}
}

func (m *MockStore) Close() error {
	return nil
}

func (m *MockStore) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	return nil, nil
}

func (m *MockStore) Exec(ctx context.Context, query string, args ...any) error {
	return nil
}

func (m *MockStore) InsertMetrics(ctx context.Context, metrics chmodels.Metrics) error {
	return nil
}

func (m *MockStore) QueryMetrics(ctx context.Context, sandboxID, teamID string, start int64, limit int) ([]chmodels.Metrics, error) {
	return nil, nil
}
