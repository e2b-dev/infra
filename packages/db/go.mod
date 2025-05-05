module github.com/e2b-dev/infra/packages/db

go 1.23.7

require (
	github.com/e2b-dev/infra/packages/shared v0.0.0-20250324174051-3fb806938dc1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.4
	github.com/lib/pq v1.10.9
	go.uber.org/zap v1.27.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/text v0.23.0 // indirect
)

replace github.com/e2b-dev/infra/packages/shared => ../shared
