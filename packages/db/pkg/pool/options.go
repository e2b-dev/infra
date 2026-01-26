package pool

import "github.com/jackc/pgx/v5/pgxpool"

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
