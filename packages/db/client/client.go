package client

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports

	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	database "github.com/e2b-dev/infra/packages/db/queries"
)

type Client struct {
	*database.Queries

	conn *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL string, options ...pool.Option) (*Client, error) {
	connPool, err := pool.New(ctx, databaseURL, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}
	queries := database.New(connPool)

	return &Client{Queries: queries, conn: connPool}, nil
}

func (db *Client) Close() error {
	db.conn.Close()

	return nil
}

// WithTx runs the given function in a transaction.
func (db *Client) WithTx(ctx context.Context) (*Client, pgx.Tx, error) {
	tx, err := db.conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}

	client := &Client{Queries: db.Queries.WithTx(tx), conn: db.conn}

	return client, tx, nil
}
