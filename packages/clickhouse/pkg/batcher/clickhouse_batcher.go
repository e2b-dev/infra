package batcher

import (
	"context"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

type ClickhouseBatcher interface {
	Push(event clickhouse.SandboxEvent) error
	Close(ctx context.Context) error
}
