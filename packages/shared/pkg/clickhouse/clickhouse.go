package clickhouse

import (
	"context"
	"crypto/tls"
	"os"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var (
	connectionString = os.Getenv("CLICKHOUSE_CONNECTION_STRING")
	username         = os.Getenv("CLICKHOUSE_USERNAME")
	password         = os.Getenv("CLICKHOUSE_PASSWORD")
	database         = os.Getenv("CLICKHOUSE_DATABASE")
	debug            = os.Getenv("CLICKHOUSE_DEBUG") == "true"
)

type Store[T any] interface {
	Close() error

	InsertRow(ctx context.Context, row T) error
	InsertRows(ctx context.Context, rows []T) error

	QueryRow(ctx context.Context, query string) (T, error)
	QueryRows(ctx context.Context, query string) ([]T, error)

	Exec(ctx context.Context, query string) error
}

type ClickHouseStore[T any] struct {
	TableName string
	Conn      driver.Conn
}

func NewConn() (driver.Conn, error) {
	var (
		ctx       = context.Background()
		conn, err = ch.Open(&ch.Options{
			Addr:     []string{connectionString},
			Protocol: ch.Native,
			TLS:      &tls.Config{}, // Not using TLS for now
			Auth: ch.Auth{
				Database: database,
				Username: username,
				Password: password,
			},
			DialTimeout: time.Second * 5,
			ReadTimeout: time.Second * 90,
			Debug:       debug,
			Compression: &ch.Compression{
				Method: ch.CompressionLZ4,
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

func NewStore[T any](tableName string) (Store[T], error) {
	conn, err := NewConn()
	if err != nil {
		return nil, err
	}

	return &ClickHouseStore[T]{Conn: conn, TableName: tableName}, nil
}

func (c *ClickHouseStore[T]) Close() error {
	return c.Conn.Close()
}

func (c *ClickHouseStore[T]) InsertRow(ctx context.Context, row T) error {
	return c.Conn.AsyncInsert(ctx, "INSERT INTO "+c.TableName, true, row)
}

func (c *ClickHouseStore[T]) InsertRows(ctx context.Context, rows []T) error {
	batch, err := c.Conn.PrepareBatch(ctx, "INSERT INTO "+c.TableName)
	if err != nil {
		return err
	}
	for _, row := range rows {
		err := batch.AppendStruct(&row)
		if err != nil {
			return err
		}
	}

	return batch.Send()
}

func (c *ClickHouseStore[T]) QueryRow(ctx context.Context, query string) (T, error) {
	rows, err := c.Conn.Query(ctx, query)
	if err != nil {
		var zero T
		return zero, err
	}

	var row T
	if err := rows.ScanStruct(&row); err != nil {
		var zero T
		return zero, err
	}

	return row, nil
}

func (c *ClickHouseStore[T]) QueryRows(ctx context.Context, query string) ([]T, error) {
	rows, err := c.Conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}

	var result []T
	for rows.Next() {
		var row T
		if err := rows.ScanStruct(&row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}

	return result, nil
}

func (c *ClickHouseStore[T]) Exec(ctx context.Context, query string) error {
	return c.Conn.Exec(ctx, query)
}
