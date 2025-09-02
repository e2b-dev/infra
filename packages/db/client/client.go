package client

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
	"go.uber.org/zap"

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
		zap.L().Error("Unable to parse database URL", zap.Error(err))

		return nil, err
	}

	// Set the default number of connections
	for _, option := range options {
		option(config)
	}

	// Create the connection pool
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		zap.L().Error("Unable to create connection pool", zap.Error(err))
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
