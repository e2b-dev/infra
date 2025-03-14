package pkg

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	_ "github.com/lib/pq"

	database "github.com/e2b-dev/infra/packages/db/pkg/queries"
)

type Client struct {
	*database.Queries
	ctx  context.Context
	conn *pgxpool.Pool
}

var databaseURL = os.Getenv("POSTGRES_CONNECTION_STRING")

func NewClient(ctx context.Context) (*Client, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is empty")
	}

	// Parse the connection pool configuration
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.Fatalf("Unable to parse database URL: %v", err)
	}

	// Set the maximum number of connections
	config.MaxConns = 10 // Replace 10 with your desired max connections

	// Create the connection pool
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.Fatalf("Unable to create connection pool: %v", err)
	}
	defer pool.Close()

	queries := database.New(pool)

	return &Client{Queries: queries, ctx: ctx, conn: pool}, nil
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

	client := &Client{Queries: db.Queries.WithTx(tx), conn: db.conn, ctx: db.ctx}
	return client, tx, nil
}
