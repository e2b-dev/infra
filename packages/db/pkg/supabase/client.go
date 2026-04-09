package supabasedb

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports

	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	supabasequeries "github.com/e2b-dev/infra/packages/db/pkg/supabase/queries"
)

const poolName = "supabase"

type Client struct {
	Write     *supabasequeries.Queries
	writeConn *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL string, options ...pool.Option) (*Client, error) {
	writeClient, writePool, err := pool.New(ctx, databaseURL, poolName, options...)
	if err != nil {
		return nil, err
	}

	writeQueries := supabasequeries.New(writeClient)

	return &Client{Write: writeQueries, writeConn: writePool}, nil
}

func (db *Client) Close() error {
	db.writeConn.Close()

	return nil
}

func (db *Client) WritePool() *pgxpool.Pool {
	return db.writeConn
}

func (db *Client) WithTx(ctx context.Context) (*supabasequeries.Queries, pgx.Tx, error) {
	tx, err := db.writeConn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}

	return db.Write.WithTx(tx), tx, nil
}

func (db *Client) TestsRawSQL(ctx context.Context, sql string, args ...any) error {
	_, err := db.writeConn.Exec(ctx, sql, args...)

	return err
}

func (db *Client) TestsRawSQLQuery(ctx context.Context, sql string, processRows func(pgx.Rows) error, args ...any) error {
	rows, err := db.writeConn.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	return processRows(rows)
}
