package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	"github.com/e2b-dev/infra/packages/shared/pkg/events"
	flags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const InsertSandboxEventQuery = `INSERT INTO sandbox_events
(
    timestamp,
    sandbox_id, 
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_build_id,
    sandbox_team_id,
    event_data,
    type,
    version,
    id
)
VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?
)`

type ClickhouseDelivery struct {
	batcher *batcher.Batcher[SandboxEvent]
	conn    driver.Conn
}

func NewDefaultClickhouseSandboxEventsDelivery(ctx context.Context, conn driver.Conn, featureFlags *flags.Client) (*ClickhouseDelivery, error) {
	maxBatchSize := featureFlags.IntFlag(ctx, flags.ClickhouseBatcherMaxBatchSize)

	maxDelay := time.Duration(featureFlags.IntFlag(ctx, flags.ClickhouseBatcherMaxDelay)) * time.Millisecond

	batcherQueueSize := featureFlags.IntFlag(ctx, flags.ClickhouseBatcherQueueSize, flags.SandboxContext("clickhouse-batcher"))

	return NewClickhouseSandboxEventsDelivery(
		ctx, conn, batcher.BatcherOptions{
			MaxBatchSize: maxBatchSize,
			MaxDelay:     maxDelay,
			QueueSize:    batcherQueueSize,
			ErrorHandler: func(err error) {
				logger.L().Error(ctx, "error batching sandbox events", zap.Error(err))
			},
		},
	)
}

func NewClickhouseSandboxEventsDelivery(ctx context.Context, conn driver.Conn, opts batcher.BatcherOptions) (*ClickhouseDelivery, error) {
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

func (c *ClickhouseDelivery) Publish(_ context.Context, _ string, event events.SandboxEvent) error {
	eventDataJson, err := json.Marshal(event.EventData)
	if err != nil {
		return err
	}

	eventData := string(eventDataJson)
	ok, err := c.batcher.Push(SandboxEvent{
		Version:   event.Version,
		ID:        event.ID,
		Type:      event.Type,
		Timestamp: event.Timestamp,

		EventData:          sql.NullString{String: eventData, Valid: eventData != ""},
		SandboxID:          event.SandboxID,
		SandboxTemplateID:  event.SandboxTemplateID,
		SandboxBuildID:     event.SandboxBuildID,
		SandboxTeamID:      event.SandboxTeamID,
		SandboxExecutionID: event.SandboxExecutionID,
	})
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

func (c *ClickhouseDelivery) batchInserter(ctx context.Context, events []SandboxEvent) error {
	batch, err := c.conn.PrepareBatch(ctx, InsertSandboxEventQuery, driver.WithReleaseConnection())
	if err != nil {
		return fmt.Errorf("error preparing batch: %w", err)
	}

	for _, event := range events {
		err := batch.Append(
			event.Timestamp,
			event.SandboxID,
			event.SandboxExecutionID,
			event.SandboxTemplateID,
			event.SandboxBuildID,
			event.SandboxTeamID,
			event.EventData,
			event.Type,
			event.Version,
			event.ID,
		)
		if err != nil {
			return fmt.Errorf("error appending %d event to batch: %w", len(events), err)
		}
	}

	err = batch.Send()
	if err != nil {
		return fmt.Errorf("error sending %d events batch: %w", len(events), err)
	}

	return nil
}
