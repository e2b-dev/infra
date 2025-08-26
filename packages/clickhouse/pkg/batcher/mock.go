package batcher

import (
	"context"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

type NoopSandboxEventBatcher struct{}

func NewNoopEventBatcher() *NoopSandboxEventBatcher {
	return &NoopSandboxEventBatcher{}
}

func (m *NoopSandboxEventBatcher) Push(event clickhouse.SandboxEvent) error {
	return nil
}

func (m *NoopSandboxEventBatcher) Close(ctx context.Context) error {
	return nil
}
