package batcher

import (
	"context"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

type NoopBatcher struct{}

func NewNoopBatcher() *NoopBatcher {
	return &NoopBatcher{}
}

func (m *NoopBatcher) Push(event clickhouse.SandboxEvent) error {
	return nil
}

func (m *NoopBatcher) Close(ctx context.Context) error {
	return nil
}
