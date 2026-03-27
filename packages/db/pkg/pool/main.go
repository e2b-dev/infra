package pool

import (
	"context"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/db/pkg/retry"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
)

func New(ctx context.Context, databaseURL string, poolName string, options ...Option) (types.DBTX, *pgxpool.Pool, error) {
	// Parse the connection pool configuration
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse connection pool config: %w", err)
	}

	retryConfig := retry.DefaultConfig()

	for _, opt := range options {
		opt(config, &retryConfig)
	}

	// expose otel traces
	config.ConnConfig.Tracer = otelpgx.NewTracer()

	// Disable statement caching to avoid issues with prepared statements in transactions
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec

	// Create the connection pool
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// expose otel metrics
	if err = otelpgx.RecordStats(pool, otelpgx.WithStatsAttributes(attribute.String("pool.name", poolName))); err != nil {
		pool.Close()

		return nil, nil, fmt.Errorf("failed to record stats: %w", err)
	}

	return retry.Wrap(pool, retryConfig), pool, nil
}
