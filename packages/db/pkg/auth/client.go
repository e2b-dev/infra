package authdb

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports

	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
)

type Client struct {
	Read      *authqueries.Queries
	Write     *authqueries.Queries
	writeConn *pgxpool.Pool
	readConn  *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL, replicaURL string, options ...pool.Option) (*Client, error) {
	writeClient, writePool, err := pool.New(ctx, databaseURL, options...)
	if err != nil {
		return nil, err
	}

	writeQueries := authqueries.New(writeClient)
	readPool := writePool
	readQueries := writeQueries

	if strings.TrimSpace(replicaURL) != "" {
		var readClient types.DBTX
		readClient, readPool, err = pool.New(ctx, replicaURL, options...)
		if err != nil {
			writePool.Close()

			return nil, err
		}

		readQueries = authqueries.New(readClient)
	}

	return &Client{Read: readQueries, Write: writeQueries, writeConn: writePool, readConn: readPool}, nil
}

func (db *Client) Close() error {
	db.writeConn.Close()

	if db.readConn != nil {
		db.readConn.Close()
	}

	return nil
}

// WithTx runs the given function in a transaction.
func (db *Client) WithTx(ctx context.Context) (*authqueries.Queries, pgx.Tx, error) {
	tx, err := db.writeConn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}

	return db.Write.WithTx(tx), tx, nil
}

// TestsRawSQL executes raw SQL for tests
func (db *Client) TestsRawSQL(ctx context.Context, sql string, args ...any) error {
	_, err := db.writeConn.Exec(ctx, sql, args...)

	return err
}

// TestsRawSQLQuery executes raw SQL query and processes rows with the given function
func (db *Client) TestsRawSQLQuery(ctx context.Context, sql string, processRows func(pgx.Rows) error, args ...any) error {
	rows, err := db.writeConn.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	return processRows(rows)
}
