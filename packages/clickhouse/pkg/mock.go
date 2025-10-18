package clickhouse

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type NoopClient struct{}

var _ Clickhouse = (*NoopClient)(nil)

func NewNoopClient() *NoopClient {
	return &NoopClient{}
}

func (m *NoopClient) Close(context.Context) error {
	return nil
}

func (m *NoopClient) QuerySandboxTimeRange(context.Context, string, string) (start time.Time, end time.Time, err error) {
	return time.Time{}, time.Time{}, nil
}

func (m *NoopClient) QuerySandboxMetrics(context.Context, string, string, time.Time, time.Time, time.Duration) ([]Metrics, error) {
	return nil, nil
}

func (m *NoopClient) QueryLatestMetrics(context.Context, []string, string) ([]Metrics, error) {
	return nil, nil
}

func (m *NoopClient) ExistsSandboxId(context.Context, string) (bool, error) {
	return false, nil
}

func (m *NoopClient) SelectSandboxEventsBySandboxId(context.Context, string, int, int, bool) ([]SandboxEvent, error) {
	return nil, nil
}

func (m *NoopClient) SelectSandboxEventsByTeamId(context.Context, uuid.UUID, int, int, bool) ([]SandboxEvent, error) {
	return nil, nil
}

func (m *NoopClient) QueryTeamMetrics(context.Context, string, time.Time, time.Time, time.Duration) ([]TeamMetrics, error) {
	return nil, nil
}

func (m *NoopClient) QueryMaxStartRateTeamMetrics(context.Context, string, time.Time, time.Time, time.Duration) (MaxTeamMetric, error) {
	return MaxTeamMetric{}, nil
}

func (m *NoopClient) QueryMaxConcurrentTeamMetrics(context.Context, string, time.Time, time.Time) (MaxTeamMetric, error) {
	return MaxTeamMetric{}, nil
}
