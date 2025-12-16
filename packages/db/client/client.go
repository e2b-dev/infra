package client

import (
	"context"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports

	database "github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Client struct {
	*database.Queries

	conn *pgxpool.Pool
}

type Option func(config *pgxpool.Config)

func WithMaxConnections(maxConns int32) Option {
	return func(config *pgxpool.Config) {
		config.MaxConns = maxConns
	}
}

func WithMinIdle(minIdle int32) Option {
	return func(config *pgxpool.Config) {
		config.MinIdleConns = minIdle
	}
}

func NewClient(ctx context.Context, options ...Option) (*Client, error) {
	databaseURL := utils.RequiredEnv("POSTGRES_CONNECTION_STRING", "Postgres connection string")

	// Parse the connection pool configuration
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection pool config: %w", err)
	}

	// Set the default number of connections
	for _, option := range options {
		option(config)
	}

	// expose otel traces
	config.ConnConfig.Tracer = otelpgx.NewTracer()

	// Create the connection pool
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// expose otel metrics
	if err := otelpgx.RecordStats(pool); err != nil {
		pool.Close()

		return nil, fmt.Errorf("failed to record stats: %w", err)
	}
	queries := database.New(pool)

	return &Client{Queries: queries, conn: pool}, nil
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
