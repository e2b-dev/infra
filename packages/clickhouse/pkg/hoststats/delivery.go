package hoststats

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	flags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const InsertSandboxHostStatQuery = `INSERT INTO sandbox_host_stats
(
    timestamp,
    sandbox_id,
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_build_id,
    sandbox_team_id,
    sandbox_vcpu_count,
    sandbox_memory_mb,
    firecracker_cpu_user_time,
    firecracker_cpu_system_time,
    firecracker_memory_rss,
    firecracker_memory_vms
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

type ClickhouseDelivery struct {
	batcher *batcher.Batcher[SandboxHostStat]
	conn    driver.Conn
}

func NewDefaultClickhouseHostStatsDelivery(
	ctx context.Context,
	conn driver.Conn,
	featureFlags *flags.Client,
) (*ClickhouseDelivery, error) {
	maxBatchSize := featureFlags.IntFlag(ctx, flags.ClickhouseBatcherMaxBatchSize)
	maxDelay := time.Duration(featureFlags.IntFlag(ctx, flags.ClickhouseBatcherMaxDelay)) * time.Millisecond
	batcherQueueSize := featureFlags.IntFlag(ctx, flags.ClickhouseBatcherQueueSize)

	return NewClickhouseHostStatsDelivery(
		ctx, conn, batcher.BatcherOptions{
			MaxBatchSize: maxBatchSize,
			MaxDelay:     maxDelay,
			QueueSize:    batcherQueueSize,
			ErrorHandler: func(err error) {
				logger.L().Error(ctx, "error batching sandbox host stats", zap.Error(err))
			},
		},
	)
}

func NewClickhouseHostStatsDelivery(
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

func (c *ClickhouseDelivery) Push(stat SandboxHostStat) error {
	ok, err := c.batcher.Push(stat)
	if err != nil {
		return err
	}

	if !ok {
		return batcher.ErrBatcherQueueFull
	}

	return nil
}

func (c *ClickhouseDelivery) Close(context.Context) error {
	return c.batcher.Stop()
}

func (c *ClickhouseDelivery) batchInserter(ctx context.Context, stats []SandboxHostStat) error {
	batch, err := c.conn.PrepareBatch(ctx, InsertSandboxHostStatQuery, driver.WithReleaseConnection())
	if err != nil {
		return fmt.Errorf("error preparing batch: %w", err)
	}

	for _, stat := range stats {
		err := batch.Append(
			stat.Timestamp,
			stat.SandboxID,
			stat.SandboxExecutionID,
			stat.SandboxTemplateID,
			stat.SandboxBuildID,
			stat.SandboxTeamID,
			stat.SandboxVCPUCount,
			stat.SandboxMemoryMB,
			stat.FirecrackerCPUUserTime,
			stat.FirecrackerCPUSystemTime,
			stat.FirecrackerMemoryRSS,
			stat.FirecrackerMemoryVMS,
		)
		if err != nil {
			return fmt.Errorf("error appending %d host stat to batch: %w", len(stats), err)
		}
	}

	err = batch.Send()
	if err != nil {
		return fmt.Errorf("error sending %d host stats batch: %w", len(stats), err)
	}

	return nil
}
