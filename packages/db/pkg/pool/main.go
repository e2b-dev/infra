package pool

import (
	"context"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func New(ctx context.Context, databaseURL string, options ...Option) (*pgxpool.Pool, error) {
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

	// Disable statement caching to avoid issues with prepared statements in transactions
	// config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec

	return pool, nil
}
