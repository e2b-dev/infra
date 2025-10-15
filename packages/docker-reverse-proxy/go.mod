module github.com/e2b-dev/infra/packages/docker-reverse-proxy

go 1.24.7

replace github.com/e2b-dev/infra/packages/db v0.0.0 => ../db

replace github.com/e2b-dev/infra/packages/shared v0.0.0 => ../shared

require (
	github.com/e2b-dev/infra/packages/db v0.0.0
	github.com/e2b-dev/infra/packages/shared v0.0.0
	github.com/jellydator/ttlcache/v3 v3.4.0
)

require (
	github.com/exaring/otelpgx v0.9.3 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.4 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/lib/pq v1.10.9 // indirect
	go.opentelemetry.io/auto/sdk v1.2.0 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	golang.org/x/crypto v0.42.0 // indirect
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
