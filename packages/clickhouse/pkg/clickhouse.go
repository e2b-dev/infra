package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

const (
	batchSize     = 10_000
	flushInterval = 5 * time.Second
)

type Clickhouse interface {
	Close(ctx context.Context) error

	// Metrics queries
	InsertMetrics(ctx context.Context, metrics Metrics) error
	QueryMetrics(ctx context.Context, sandboxID, teamID string, start time.Time, limit int) ([]Metrics, error)
}

type Client struct {
	conn driver.Conn

	metricsCh       chan Metrics
	metricsChClosed chan struct{}
}

func New(connectionString string) (Clickhouse, error) {
	options, err := clickhouse.ParseDSN(connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ClickHouse DSN: %w", err)
	}

	// There should be only one connection per client as we are sending data only from one goroutine, this is to prevent connection leaks.
	options.MaxOpenConns = 3
	options.MaxIdleConns = 1
	options.TLS = nil

	conn, err := clickhouse.Open(options)
	if err != nil {
		return nil, fmt.Errorf("failed to open ClickHouse connection: %w", err)
	}

	c := &Client{
		conn:            conn,
		metricsCh:       make(chan Metrics, 10_000),
		metricsChClosed: make(chan struct{}),
	}

	go c.metricsBatchLoop()

	return c, nil
}

// Close drains the queue and flushes remaining items
func (c *Client) Close(ctx context.Context) error {
	done := make(chan struct{})

	go func() {
		close(c.metricsCh)
		<-c.metricsChClosed
		close(done)
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("context cancelled while closing clickhouse client: %w", ctx.Err())
	case <-done:
		return nil
	}
}
