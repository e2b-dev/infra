package batcher

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

type SandboxEventInsertBatcher struct {
	*Batcher[clickhouse.SandboxEvent]
	conn driver.Conn
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

	batcher, err := NewBatcher(b.processInsertSandboxEventsBatch, BatcherOptions{
		MaxBatchSize: opts.MaxBatchSize,
		MaxDelay:     opts.MaxDelay,
		QueueSize:    opts.QueueSize,
	})
	if err != nil {
		return nil, err
	}

	if err := batcher.Start(); err != nil {
		return nil, err
	}

	b.Batcher = batcher

	return b, nil
}

func (b *SandboxEventInsertBatcher) processInsertSandboxEventsBatch(events []clickhouse.SandboxEvent) {
	log.Println("~~~~~processing batch of sandbox events", len(events))
	ctx := context.Background()
	batch, err := b.conn.PrepareBatch(
		ctx, InsertSandboxEventQuery, driver.WithReleaseConnection())
	if err != nil {
		return
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
			// TODO: Handle error
			return
		}
	}

	err = batch.Send()
	if err != nil {
		// TODO: Handle error
		return
	}
}

func (b *SandboxEventInsertBatcher) Push(event clickhouse.SandboxEvent) error {
	if b.Batcher.ch == nil {
		return ErrBatcherNotStarted
	}
	select {
	case b.Batcher.ch <- event:
		return nil
	default:
		return errors.New("batcher queue is full")
	}
}

func (b *SandboxEventInsertBatcher) Close(ctx context.Context) error {
	if err := b.Batcher.Stop(); err != nil {
		return fmt.Errorf("error stopping batcher: %w", err)
	}
	return b.conn.Close()
}
