package pool

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/e2b-dev/infra/packages/db/pkg/retry"
)

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
