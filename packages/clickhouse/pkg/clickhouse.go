package clickhouse

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type Clickhouse interface {
	Close(ctx context.Context) error

	// Metrics queries
	QueryLatestMetrics(ctx context.Context, sandboxIDs []string, teamID string) ([]Metrics, error)
}

type Client struct {
	conn driver.Conn

	mp *sdkmetric.MeterProvider
}

func New(connectionString string) (*Client, error) {
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

	return &Client{conn: conn}, nil
}

// Close drains the queue and flushes remaining items
func (c *Client) Close(ctx context.Context) error {
	return c.conn.Close()
}
