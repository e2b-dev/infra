module github.com/e2b-dev/infra/packages/proxy

go 1.25.4

// https://github.com/grafana/loki/issues/2826
replace github.com/hashicorp/consul => github.com/hashicorp/consul v1.14.5

// https://github.com/grafana/loki/issues/2826
replace k8s.io/client-go => k8s.io/client-go v0.28.1

// https://github.com/grafana/loki/issues/2826
replace k8s.io/api => k8s.io/api v0.28.1

// https://github.com/grafana/loki/issues/2826
exclude k8s.io/client-go v8.0.0+incompatible

// https://github.com/grafana/loki/issues/2826
replace github.com/prometheus/prometheus => github.com/prometheus/prometheus v0.47.2-0.20231010075449-4b9c19fe5510

replace (
	github.com/e2b-dev/infra/packages/clickhouse v0.0.0 => ../clickhouse
	github.com/e2b-dev/infra/packages/shared v0.0.0 => ../shared
)

require (
	github.com/caarlos0/env/v11 v11.3.1
	github.com/e2b-dev/infra/packages/shared v0.0.0
	github.com/google/uuid v1.6.0
	go.opentelemetry.io/otel/metric v1.38.0
	go.uber.org/zap v1.27.0
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gregjones/httpcache v0.0.0-20180305231024-9cad4c3443a7 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.3.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.2 // indirect
	github.com/jellydator/ttlcache/v3 v3.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/launchdarkly/ccache v1.1.0 // indirect
	github.com/launchdarkly/eventsource v1.10.0 // indirect
	github.com/launchdarkly/go-jsonstream/v3 v3.1.0 // indirect
	github.com/launchdarkly/go-sdk-common/v3 v3.3.0 // indirect
	github.com/launchdarkly/go-sdk-events/v3 v3.5.0 // indirect
	github.com/launchdarkly/go-semver v1.0.3 // indirect
	github.com/launchdarkly/go-server-sdk-evaluation/v3 v3.0.1 // indirect
	github.com/launchdarkly/go-server-sdk/v7 v7.13.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/orcaman/concurrent-map/v2 v2.0.1 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible // indirect
	github.com/redis/go-redis/extra/rediscmd/v9 v9.17.3 // indirect
	github.com/redis/go-redis/extra/redisotel/v9 v9.17.3 // indirect
	github.com/redis/go-redis/v9 v9.17.3 // indirect
	go.opentelemetry.io/auto/sdk v1.2.0 // indirect
	go.opentelemetry.io/contrib/bridges/otelzap v0.13.0 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.14.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.38.0 // indirect
	go.opentelemetry.io/otel/log v0.14.0 // indirect
	go.opentelemetry.io/otel/sdk v1.38.0 // indirect
	go.opentelemetry.io/otel/sdk/log v0.14.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	go.opentelemetry.io/proto/otlp v1.8.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20250531010427-b6e5de432a8b // indirect
	golang.org/x/mod v0.29.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250908214217-97024824d090 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250908214217-97024824d090 // indirect
	google.golang.org/grpc v1.75.1 // indirect
	google.golang.org/protobuf v1.36.9 // indirect
)
