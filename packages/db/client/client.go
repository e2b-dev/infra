package client

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports

	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	database "github.com/e2b-dev/infra/packages/db/queries"
)

const poolName = "main"

type Client struct {
	*database.Queries

	conn *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL string, options ...pool.Option) (*Client, error) {
	dbClient, connPool, err := pool.New(ctx, databaseURL, poolName, options...)
	if err != nil {
		return nil, err
	}

	return &Client{Queries: database.New(dbClient), conn: connPool}, nil
}

func (db *Client) Close() error {
	db.conn.Close()

	return nil
}

// WithTx starts a read-write transaction and returns a transactional Client.
func (db *Client) WithTx(ctx context.Context) (*Client, pgx.Tx, error) {
	tx, err := db.conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}

	client := &Client{Queries: db.Queries.WithTx(tx), conn: db.conn}

	return client, tx, nil
}
