package db

import (
	"context"
	"fmt"
	"os"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type PoolOption func(config *pgxpool.Config)

func WithMaxConnections(maxConns int) PoolOption {
	return func(config *pgxpool.Config) {
		config.MaxConns = int32(maxConns)
	}
}

func WithMinIdle(minIdle int) PoolOption {
	return func(config *pgxpool.Config) {
		config.MinIdleConns = int32(minIdle)
	}
}

func NewPool(ctx context.Context, options ...PoolOption) (*pgxpool.Pool, error) {
	databaseURL := os.Getenv("POSTGRES_CONNECTION_STRING")
	if databaseURL == "" {
		return nil, fmt.Errorf("POSTGRES_CONNECTION_STRING is not set")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		zap.L().Error("Unable to parse database URL", zap.Error(err))

		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

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
		return nil, fmt.Errorf("failed to record stats: %w", err)
	}

	return pool, nil
}
