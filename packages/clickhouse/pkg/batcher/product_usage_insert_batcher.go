package batcher

import (
	"context"
	"errors"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

type ProductUsageInsertBatcher struct {
	*Batcher[clickhouse.ProductUsage]
	errorHandler func(error)
	conn         driver.Conn
}

const InsertProductUsageQuery = `
INSERT INTO product_usage
(
    timestamp,
    team_id,
    category,
    action,
    label
)
VALUES (
    ?,
    ?,
    ?,
    ?,
    ?
)`

func NewProductUsageInsertsBatcher(conn driver.Conn, opts BatcherOptions) (*ProductUsageInsertBatcher, error) {
	b := &ProductUsageInsertBatcher{
		conn: conn,
	}

	batcher, err := NewBatcher(b.processInsertProductUsageBatch, opts)
	if err != nil {
		return nil, err
	}

	if err := batcher.Start(); err != nil {
		return nil, err
	}

	b.Batcher = batcher

	return b, nil
}

func (b *ProductUsageInsertBatcher) processInsertProductUsageBatch(events []clickhouse.ProductUsage) error {
	ctx := context.Background()
	batch, err := b.conn.PrepareBatch(
		ctx, InsertProductUsageQuery, driver.WithReleaseConnection())
	if err != nil {
		return fmt.Errorf("error preparing batch: %w", err)
	}

	for _, event := range events {
		err := batch.Append(
			event.Timestamp,
			event.TeamID,
			event.Category,
			event.Action,
			event.Label,
		)
		if err != nil {
			return fmt.Errorf("error appending product usage event to batch: %w", err)
		}
	}

	err = batch.Send()
	if err != nil {
		return fmt.Errorf("error sending product usage events batch: %w", err)
	}

	return nil
}

func (b *ProductUsageInsertBatcher) Push(event clickhouse.ProductUsage) error {
	success, err := b.Batcher.Push(event)
	if err != nil {
		return err
	}
	if !success {
		return errors.New("batcher queue is full")
	}
	return nil
}

func (b *ProductUsageInsertBatcher) Close(ctx context.Context) error {
	stopErr := b.Batcher.Stop()
	closeErr := b.conn.Close()

	var errs []error
	if stopErr != nil {
		errs = append(errs, fmt.Errorf("error stopping product usage insert batcher: %w", stopErr))
	}
	if closeErr != nil {
		errs = append(errs, fmt.Errorf("error closing product usage insert batcher connection: %w", closeErr))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
