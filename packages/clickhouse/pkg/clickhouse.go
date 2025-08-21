package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

type Clickhouse interface {
	Close(ctx context.Context) error

	// Sandbox metrics queries
	QuerySandboxTimeRange(ctx context.Context, sandboxID, teamID string) (start time.Time, end time.Time, err error)
	QuerySandboxMetrics(ctx context.Context, sandboxID, teamID string, start time.Time, end time.Time, step time.Duration) ([]Metrics, error)
	QueryLatestMetrics(ctx context.Context, sandboxIDs []string, teamID string) ([]Metrics, error)

	// Events queries
	ExistsSandboxId(ctx context.Context, sandboxID string) (bool, error)
	SelectSandboxEventsBySandboxId(ctx context.Context, sandboxID string, offset, limit int, orderAsc bool) ([]SandboxEvent, error)
	SelectSandboxEventsByTeamId(ctx context.Context, teamID uuid.UUID, offset, limit int, orderAsc bool) ([]SandboxEvent, error)

	// Team metrics queries
	QueryTeamMetrics(ctx context.Context, teamID string, start time.Time, end time.Time, step time.Duration) ([]TeamMetrics, error)
}

type Client struct {
	conn driver.Conn
}

func NewDriver(connectionString string) (driver.Conn, error) {
	options, err := clickhouse.ParseDSN(connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ClickHouse DSN: %w", err)
	}

	options.MaxOpenConns = 10
	options.MaxIdleConns = 3
	options.TLS = nil

	conn, err := clickhouse.Open(options)
	if err != nil {
		return nil, fmt.Errorf("failed to open ClickHouse connection: %w", err)
	}

	return conn, nil
}

func New(connectionString string) (*Client, error) {
	conn, err := NewDriver(connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClickHouse driver: %w", err)
	}

	return &Client{conn: conn}, nil
}

// Close drains the queue and flushes remaining items
func (c *Client) Close(ctx context.Context) error {
	return c.conn.Close()
}
