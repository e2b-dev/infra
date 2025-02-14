package chdb

import (
	"context"
	"crypto/tls"
	"os"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/chmodels"
)

var (
	connectionString = os.Getenv("CLICKHOUSE_CONNECTION_STRING")
	username         = os.Getenv("CLICKHOUSE_USERNAME")
	password         = os.Getenv("CLICKHOUSE_PASSWORD")
	database         = os.Getenv("CLICKHOUSE_DATABASE")
	debug            = os.Getenv("CLICKHOUSE_DEBUG") == "true"
)

type Store interface {
	Close() error

	// Base queries
	Query(ctx context.Context, query string) (driver.Rows, error)
	Exec(ctx context.Context, query string, args ...any) error

	// Metrics queries
	InsertMetrics(ctx context.Context, metrics chmodels.Metrics) error
	QueryMetrics(ctx context.Context, sandboxID, teamID string, start int64, limit int) ([]chmodels.Metrics, error)
}

type ClickHouseStore struct {
	Conn driver.Conn
}

func NewConn() (driver.Conn, error) {
	var (
		ctx       = context.Background()
		conn, err = clickhouse.Open(&clickhouse.Options{
			Addr:     []string{connectionString},
			Protocol: clickhouse.Native,
			TLS:      &tls.Config{}, // Not using TLS for now
			Auth: clickhouse.Auth{
				Database: database,
				Username: username,
				Password: password,
			},
			DialTimeout: time.Second * 5,
			ReadTimeout: time.Second * 90,
			Debug:       debug,
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

func NewStore() (Store, error) {
	conn, err := NewConn()
	if err != nil {
		return nil, err
	}

	return &ClickHouseStore{conn}, nil
}

func (c *ClickHouseStore) Close() error {
	return c.Conn.Close()
}

func (c *ClickHouseStore) Query(ctx context.Context, query string) (driver.Rows, error) {
	return c.Conn.Query(ctx, query)
}

func (c *ClickHouseStore) Exec(ctx context.Context, query string, args ...any) error {
	return c.Conn.Exec(ctx, query, args...)
}
