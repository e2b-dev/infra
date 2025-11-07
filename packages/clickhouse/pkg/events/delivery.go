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
)

const InsertSandboxEventQuery = `INSERT INTO sandbox_events
(
    timestamp,
    sandbox_id, 
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_build_id,
    sandbox_team_id,
    event_category,
    event_label,
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
    ?,
    ?,
    ?
)`

type ClickhouseDelivery struct {
	batcher *batcher.Batcher[SandboxEvent]
	conn    driver.Conn
}

func NewDefaultClickhouseSandboxEventsDelivery(ctx context.Context, conn driver.Conn, featureFlags *flags.Client) (*ClickhouseDelivery, error) {
	maxBatchSize := 100
	if val, err := featureFlags.IntFlag(ctx, flags.ClickhouseBatcherMaxBatchSize); err == nil {
		maxBatchSize = val
	}

	maxDelay := 1 * time.Second
	if val, err := featureFlags.IntFlag(ctx, flags.ClickhouseBatcherMaxDelay); err == nil {
		maxDelay = time.Duration(val) * time.Millisecond
	}

	bactherQueueSize := 1000
	if val, err := featureFlags.IntFlag(ctx, flags.ClickhouseBatcherQueueSize, flags.SandboxContext("clickhouse-batcher")); err == nil {
		bactherQueueSize = val
	}

	return NewClickhouseSandboxEventsDelivery(
		conn,
		batcher.BatcherOptions{
			MaxBatchSize: maxBatchSize,
			MaxDelay:     maxDelay,
			QueueSize:    bactherQueueSize,
			ErrorHandler: func(err error) {
				zap.L().Error("error batching sandbox events", zap.Error(err))
			},
		},
	)
}

func NewClickhouseSandboxEventsDelivery(conn driver.Conn, opts batcher.BatcherOptions) (*ClickhouseDelivery, error) {
	var err error

	delivery := &ClickhouseDelivery{conn: conn}
	delivery.batcher, err = batcher.NewBatcher(delivery.batchInserter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create batcher: %w", err)
	}

	if err = delivery.batcher.Start(); err != nil {
		return nil, fmt.Errorf("failed to start batcher: %w", err)
	}

	return delivery, nil
}

func (c *ClickhouseDelivery) Publish(ctx context.Context, deliveryKey string, event events.SandboxEvent) error {
	eventData := ""
	eventDataJson, err := json.Marshal(event.EventData)
	if err != nil {
		zap.L().Error("Error marshalling sandbox event data", zap.Error(err))
	} else {
		eventData = string(eventDataJson)
	}

	ok, err := c.batcher.Push(SandboxEvent{
		Version:   event.Version,
		ID:        event.ID,
		Type:      event.Type,
		Timestamp: event.Timestamp,

		EventCategory: event.EventCategory,
		EventLabel:    event.EventLabel,
		EventData:     sql.NullString{String: eventData, Valid: eventData != ""},

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

func (c *ClickhouseDelivery) batchInserter(events []SandboxEvent) error {
	ctx := context.Background()
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
			event.EventCategory,
			event.EventLabel,
			event.EventData,
			event.Type,
			event.Version,
			event.ID,
		)
		if err != nil {
			return fmt.Errorf("error appending %d product usage event to batch: %w", len(events), err)
		}
	}

	err = batch.Send()
	if err != nil {
		return fmt.Errorf("error sending %d sandbox events batch: %w", len(events), err)
	}

	return nil
}
