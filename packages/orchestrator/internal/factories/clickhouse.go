package factories

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	"github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func NewSandboxInsertsEventBatcher(ctx context.Context, clickhouseConn driver.Conn, featureFlags *feature_flags.Client) (*batcher.SandboxEventInsertBatcher, error) {
	maxBatchSize := 100
	if val, err := featureFlags.IntFlag(ctx, feature_flags.ClickhouseBatcherMaxBatchSize); err == nil {
		maxBatchSize = val
	}

	maxDelay := 1 * time.Second
	if val, err := featureFlags.IntFlag(ctx, feature_flags.ClickhouseBatcherMaxDelay); err == nil {
		maxDelay = time.Duration(val) * time.Millisecond
	}

	bactherQueueSize := 1000
	if val, err := featureFlags.IntFlag(ctx, feature_flags.ClickhouseBatcherQueueSize, feature_flags.SandboxContext("clickhouse-batcher")); err == nil {
		bactherQueueSize = val
	}

	return batcher.NewSandboxEventInsertsBatcher(clickhouseConn, batcher.BatcherOptions{
		MaxBatchSize: maxBatchSize,
		MaxDelay:     maxDelay,
		QueueSize:    bactherQueueSize,
		ErrorHandler: func(err error) {
			zap.L().Error("error batching sandbox events", zap.Error(err))
		},
	})
}
