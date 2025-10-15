module github.com/e2b-dev/infra/packages/docker-reverse-proxy

go 1.24.7

replace github.com/e2b-dev/infra/packages/shared v0.0.0 => ../shared

replace github.com/e2b-dev/infra/packages/db v0.0.0 => ../db

require (
	github.com/e2b-dev/infra/packages/db v0.0.0
	github.com/e2b-dev/infra/packages/shared v0.0.0
	github.com/jellydator/ttlcache/v3 v3.4.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.4 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/lib/pq v1.10.9 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.42.0 // indirect
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
