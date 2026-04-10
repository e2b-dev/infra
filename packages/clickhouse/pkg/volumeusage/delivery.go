package volumeusage

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const InsertVolumeUsageSnapshotQuery = `INSERT INTO volume_usage_snapshots
(
    timestamp,
    team_id,
    volume_id,
    usage_bytes,
    quota_bytes,
    is_blocked
)
VALUES (?, ?, ?, ?, ?, ?)`

type ClickhouseDelivery struct {
	batcher *batcher.Batcher[VolumeUsageSnapshot]
	conn    driver.Conn
}

func NewDefaultClickhouseVolumeUsageDelivery(
	ctx context.Context,
	conn driver.Conn,
	featureFlags *featureflags.Client,
) (*ClickhouseDelivery, error) {
	maxBatchSize := featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherMaxBatchSize)
	maxDelay := time.Duration(featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherMaxDelay)) * time.Millisecond
	batcherQueueSize := featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherQueueSize)

	return NewClickhouseVolumeUsageDelivery(
		ctx, conn, batcher.BatcherOptions{
			MaxBatchSize: maxBatchSize,
			MaxDelay:     maxDelay,
			QueueSize:    batcherQueueSize,
			ErrorHandler: func(err error) {
				logger.L().Error(ctx, "error batching volume usage snapshots", zap.Error(err))
			},
		},
	)
}

func NewClickhouseVolumeUsageDelivery(
	ctx context.Context,
	conn driver.Conn,
	opts batcher.BatcherOptions,
) (*ClickhouseDelivery, error) {
	var err error

	delivery := &ClickhouseDelivery{conn: conn}
	delivery.batcher, err = batcher.NewBatcher(delivery.batchInserter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create batcher: %w", err)
	}

	if err = delivery.batcher.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start batcher: %w", err)
	}

	return delivery, nil
}

func (c *ClickhouseDelivery) Push(snapshot VolumeUsageSnapshot) error {
	return c.batcher.Push(snapshot)
}

func (c *ClickhouseDelivery) Close(context.Context) error {
	return c.batcher.Stop()
}

func (c *ClickhouseDelivery) batchInserter(ctx context.Context, snapshots []VolumeUsageSnapshot) error {
	batch, err := c.conn.PrepareBatch(ctx, InsertVolumeUsageSnapshotQuery, driver.WithReleaseConnection())
	if err != nil {
		return fmt.Errorf("error preparing batch: %w", err)
	}

	for _, snapshot := range snapshots {
		// Convert bool to UInt8 for ClickHouse
		var isBlocked uint8
		if snapshot.IsBlocked {
			isBlocked = 1
		}

		err := batch.Append(
			snapshot.Timestamp,
			snapshot.TeamID,
			snapshot.VolumeID,
			snapshot.UsageBytes,
			snapshot.QuotaBytes,
			isBlocked,
		)
		if err != nil {
			return fmt.Errorf("error appending volume usage snapshot to batch: %w", err)
		}
	}

	err = batch.Send()
	if err != nil {
		return fmt.Errorf("error sending %d volume usage snapshots batch: %w", len(snapshots), err)
	}

	return nil
}
