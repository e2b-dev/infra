package batcher

import (
	"context"
	"errors"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

type SandboxEventInsertBatcher struct {
	*Batcher[clickhouse.SandboxEvent]
	errorHandler func(error)
	conn         driver.Conn
}

const InsertSandboxEventQuery = `
INSERT INTO sandbox_events
(
    timestamp,
    sandbox_id, 
    sandbox_execution_id,
    sandbox_template_id,
    sandbox_build_id,
    sandbox_team_id,
    event_category,
    event_label,
    event_data
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
    ?
)`

func NewSandboxEventInsertsBatcher(conn driver.Conn, opts BatcherOptions) (*SandboxEventInsertBatcher, error) {
	b := &SandboxEventInsertBatcher{
		conn: conn,
	}

	batcher, err := NewBatcher(b.processInsertSandboxEventsBatch, opts)
	if err != nil {
		return nil, err
	}

	if err := batcher.Start(); err != nil {
		return nil, err
	}

	b.Batcher = batcher

	return b, nil
}

func (b *SandboxEventInsertBatcher) processInsertSandboxEventsBatch(events []clickhouse.SandboxEvent) error {
	ctx := context.Background()
	batch, err := b.conn.PrepareBatch(
		ctx, InsertSandboxEventQuery, driver.WithReleaseConnection())
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

func (b *SandboxEventInsertBatcher) Push(event clickhouse.SandboxEvent) error {
	success, err := b.Batcher.Push(event)
	if err != nil {
		return err
	}
	if !success {
		return ErrBatcherQueueFull
	}
	return nil
}

func (b *SandboxEventInsertBatcher) Close(ctx context.Context) error {
	stopErr := b.Batcher.Stop()
	closeErr := b.conn.Close()

	var errs []error
	if stopErr != nil {
		errs = append(errs, fmt.Errorf("error stopping sandbox event insert batcher: %w", stopErr))
	}
	if closeErr != nil {
		errs = append(errs, fmt.Errorf("error closing sandbox event insert batcher connection: %w", closeErr))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
