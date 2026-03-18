package dashboarddb

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports

	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
)

const poolName = "dashboard"

type Client struct {
	*dashboardqueries.Queries

	conn *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL string, options ...pool.Option) (*Client, error) {
	dbClient, connPool, err := pool.New(ctx, databaseURL, poolName, options...)
	if err != nil {
		return nil, err
	}

	return &Client{Queries: dashboardqueries.New(dbClient), conn: connPool}, nil
}

func (db *Client) Close() error {
	db.conn.Close()

	return nil
}

func (db *Client) WithTx(ctx context.Context) (*Client, pgx.Tx, error) {
	tx, err := db.conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}

	client := &Client{Queries: db.Queries.WithTx(tx), conn: db.conn}

	return client, tx, nil
}
