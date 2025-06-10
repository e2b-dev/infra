package chdb

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/e2b-dev/infra/packages/shared/pkg/chdb/chmodels"
)

type Store interface {
	Close() error

	// Base queries
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
	Exec(ctx context.Context, query string, args ...any) error

	// Metrics queries
	InsertMetrics(ctx context.Context, metrics chmodels.Metrics) error
	QueryMetrics(ctx context.Context, sandboxID, teamID string, start int64, limit int) ([]chmodels.Metrics, error)
}

type ClickHouseStore struct {
	Conn driver.Conn
}

func NewConn(config ClickHouseConfig) (driver.Conn, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate ClickHouse config: %w", err)
	}

	var (
		ctx       = context.Background()
		conn, err = clickhouse.Open(&clickhouse.Options{
			Addr:     []string{config.ConnectionString},
			Protocol: clickhouse.Native,
			TLS:      &tls.Config{}, // Not using TLS for now
			Auth: clickhouse.Auth{
				Database: config.Database,
				Username: config.Username,
				Password: config.Password,
			},
			DialTimeout: time.Second * 5,
			ReadTimeout: time.Second * 90,
			Debug:       config.Debug,
			Compression: &clickhouse.Compression{
				Method: clickhouse.CompressionLZ4,
			},
		})
	)

	if err != nil {
		return nil, err
	}

	if err := conn.Ping(ctx); err != nil {
		return nil, err
	}

	return conn, nil
}

func NewStore(config ClickHouseConfig) (Store, error) {
	conn, err := NewConn(config)
	if err != nil {
		return nil, err
	}

	return &ClickHouseStore{conn}, nil
}

func (c *ClickHouseStore) Close() error {
	return c.Conn.Close()
}

func (c *ClickHouseStore) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	return c.Conn.Query(ctx, query, args...)
}

func (c *ClickHouseStore) Exec(ctx context.Context, query string, args ...any) error {
	return c.Conn.Exec(ctx, query, args...)
}
