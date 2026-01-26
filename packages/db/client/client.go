package client

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports

	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	database "github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/retry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Client struct {
	*database.Queries

	conn *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL string, options ...pool.Option) (*Client, error) {
	connPool, err := pool.New(ctx, databaseURL, options...)
type Option func(config *pgxpool.Config, retryConfig *retry.Config)

func WithMaxConnections(maxConns int32) Option {
	return func(config *pgxpool.Config, _ *retry.Config) {
		config.MaxConns = maxConns
	}
}

func WithMinIdle(minIdle int32) Option {
	return func(config *pgxpool.Config, _ *retry.Config) {
		config.MinIdleConns = minIdle
	}
}

// WithRetryConfig sets custom retry configuration.
// If not provided, default retry configuration is used.
func WithRetryConfig(opts ...retry.Option) Option {
	return func(_ *pgxpool.Config, cfg *retry.Config) {
		cfg.Apply(opts...)
	}
}

func NewClient(ctx context.Context, options ...Option) (*Client, error) {
	databaseURL := utils.RequiredEnv("POSTGRES_CONNECTION_STRING", "Postgres connection string")

	return NewClientFromConnectionString(ctx, databaseURL, options...)
}

func NewClientFromConnectionString(ctx context.Context, databaseURL string, options ...Option) (*Client, error) {
	// Parse the connection pool configuration
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection pool config: %w", err)
	}

	// Configure retry behavior
	retryConfig := retry.DefaultConfig()

	// Set the default number of connections
	for _, option := range options {
		option(config, &retryConfig)
	}

	// expose otel traces
	config.ConnConfig.Tracer = otelpgx.NewTracer()

	// Create the connection pool
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}
	queries := database.New(connPool)

	return &Client{Queries: queries, conn: connPool}, nil
		return nil, fmt.Errorf("failed to record stats: %w", err)
	}

	// Wrap the pool with retry logic
	retryableDB := retry.Wrap(pool, retryConfig)
	queries := database.New(retryableDB)

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
