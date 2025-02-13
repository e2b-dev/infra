package chdb

import (
	"context"
	"crypto/tls"
	"os"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
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

	Insert(ctx context.Context, rows []any, table string) error
	Query(ctx context.Context, query string) (driver.Rows, error)
	Exec(ctx context.Context, query string) error
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

func NewStore(tableName string) (Store, error) {
	conn, err := NewConn()
	if err != nil {
		return nil, err
	}

	return &ClickHouseStore{conn}, nil
}

func (c *ClickHouseStore) Close() error {
	return c.Conn.Close()
}

func (c *ClickHouseStore) Insert(ctx context.Context, rows []any, table string) error {
	batch, err := c.Conn.PrepareBatch(ctx, "INSERT INTO "+table)
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

func (c *ClickHouseStore) Query(ctx context.Context, query string) (driver.Rows, error) {
	return c.Conn.Query(ctx, query)
}

func (c *ClickHouseStore) Exec(ctx context.Context, query string) error {
	return c.Conn.Exec(ctx, query)
}

/*
 * Helper Generic Functions
 * note: type must be a struct or slice of structs
 */

type QueryTypeConstraint interface {
	struct{} | []struct{}
}

func TypedQuery[T QueryTypeConstraint](ctx context.Context, store *ClickHouseStore, query string) (T, error) {
	rows, err := store.Query(ctx, query)
	if err != nil {
		var zero T
		return zero, err
	}

	var scannedRow T
	if err := rows.ScanStruct(&scannedRow); err != nil {
		var zero T
		return zero, err
	}

	return scannedRow, nil
}

type InsertTypeConstraint interface {
	[]struct{}
}

func TypedInsert[T InsertTypeConstraint](ctx context.Context, store *ClickHouseStore, rows []T, table string) error {
	anyRows := make([]any, len(rows))
	for i := range rows {
		anyRows[i] = rows[i]
	}
	return store.Insert(ctx, anyRows, table)
}
