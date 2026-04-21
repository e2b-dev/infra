package hoststats

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
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
    cgroup_cpu_usage_usec,
    cgroup_cpu_user_usec,
    cgroup_cpu_system_usec,
    cgroup_memory_usage_bytes,
    cgroup_memory_peak_bytes,
    delta_cgroup_cpu_usage_usec,
    delta_cgroup_cpu_user_usec,
    delta_cgroup_cpu_system_usec,
    interval_us,
    sandbox_type
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

type ClickhouseDelivery struct {
	batcher *batcher.Batcher[SandboxHostStat]
	conn    driver.Conn
}

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/clickhouse/pkg/hoststats")

func NewDefaultClickhouseHostStatsDelivery(
	ctx context.Context,
	conn driver.Conn,
	featureFlags *featureflags.Client,
) (*ClickhouseDelivery, error) {
	maxBatchSize := featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherMaxBatchSize)
	maxDelay := time.Duration(featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherMaxDelay)) * time.Millisecond
	batcherQueueSize := featureFlags.IntFlag(ctx, featureflags.ClickhouseBatcherQueueSize)

	return NewClickhouseHostStatsDelivery(
		ctx, conn, batcher.BatcherOptions{
			Name:         "sandbox-host-stats",
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
	delivery := &ClickhouseDelivery{conn: conn}

	var err error
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
	return c.batcher.Push(stat)
}

func (c *ClickhouseDelivery) Close(context.Context) error {
	return c.batcher.Stop()
}

func (c *ClickhouseDelivery) batchInserter(ctx context.Context, stats []SandboxHostStat) error {
	attrs := trace.WithAttributes(attribute.Int("batch.size", len(stats)))
	ctx, span := tracer.Start(ctx, "Flush host stats batch to Clickhouse", attrs)
	defer span.End()

	batch, err := c.conn.PrepareBatch(ctx, InsertSandboxHostStatQuery, driver.WithReleaseConnection())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "prepare batch failed")

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
			stat.CgroupCPUUsageUsec,
			stat.CgroupCPUUserUsec,
			stat.CgroupCPUSystemUsec,
			stat.CgroupMemoryUsage,
			stat.CgroupMemoryPeak,
			stat.DeltaCgroupCPUUsageUsec,
			stat.DeltaCgroupCPUUserUsec,
			stat.DeltaCgroupCPUSystemUsec,
			stat.IntervalUs,
			stat.SandboxType,
		)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "append failed")

			return fmt.Errorf("error appending %d host stat to batch: %w", len(stats), err)
		}
	}

	if err = batch.Send(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "send failed")

		return fmt.Errorf("error sending %d host stats batch: %w", len(stats), err)
	}

	return nil
}
