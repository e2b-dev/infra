module github.com/e2b-dev/infra/packages/api

go 1.25.4

// Internal packages
replace (
	github.com/e2b-dev/infra/packages/clickhouse v0.0.0 => ../clickhouse
	github.com/e2b-dev/infra/packages/db v0.0.0 => ../db
	github.com/e2b-dev/infra/packages/shared v0.0.0 => ../shared
)

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

tool (
	github.com/air-verse/air
	github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
)

require (
	github.com/Masterminds/semver/v3 v3.4.0
	github.com/bsm/redislock v0.9.4
	github.com/caarlos0/env/v11 v11.3.1
	github.com/e2b-dev/infra/packages/clickhouse v0.0.0
	github.com/e2b-dev/infra/packages/db v0.0.0
	github.com/e2b-dev/infra/packages/shared v0.0.0
	github.com/flowchartsman/retry v1.2.0
	github.com/getkin/kin-openapi v0.132.0
	github.com/gin-contrib/cors v1.7.5
	github.com/gin-contrib/size v1.0.2
	github.com/gin-gonic/gin v1.10.1
	github.com/gogo/status v1.1.1
	github.com/golang-jwt/jwt/v5 v5.2.2
	github.com/golang/protobuf v1.5.4
	github.com/google/uuid v1.6.0
	github.com/hashicorp/nomad/api v0.0.0-20231208134655-099ee06a607c
	github.com/jackc/pgx/v5 v5.7.4
	github.com/jellydator/ttlcache/v3 v3.4.0
	github.com/launchdarkly/go-sdk-common/v3 v3.3.0
	github.com/oapi-codegen/gin-middleware v1.0.2
	github.com/oapi-codegen/runtime v1.1.1
	github.com/orcaman/concurrent-map/v2 v2.0.1
	github.com/pkg/errors v0.9.1
	github.com/posthog/posthog-go v0.0.0-20230801140217-d607812dee69
	github.com/pressly/goose/v3 v3.24.2
	github.com/redis/go-redis/v9 v9.12.1
	github.com/stretchr/testify v1.11.1
	go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin v0.57.0
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.63.0
	go.opentelemetry.io/otel v1.38.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.38.0
	go.opentelemetry.io/otel/metric v1.38.0
	go.opentelemetry.io/otel/sdk/metric v1.38.0
	go.opentelemetry.io/otel/trace v1.38.0
	go.uber.org/zap v1.27.0
	golang.org/x/net v0.47.0
	golang.org/x/sync v0.18.0
	google.golang.org/grpc v1.75.1
	google.golang.org/protobuf v1.36.9
)

require (
	dario.cat/mergo v1.0.2 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/ClickHouse/ch-go v0.66.1 // indirect
	github.com/ClickHouse/clickhouse-go/v2 v2.37.2 // indirect
	github.com/DataDog/datadog-go/v5 v5.2.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/air-verse/air v1.61.7 // indirect
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/bep/godartsass/v2 v2.3.2 // indirect
	github.com/bep/golibsass v1.2.0 // indirect
	github.com/bytedance/sonic v1.13.2 // indirect
	github.com/bytedance/sonic/loader v0.2.4 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cli/safeexec v1.0.1 // indirect
	github.com/cloudwego/base64x v0.1.5 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/platforms v0.2.1 // indirect
	github.com/cpuguy83/dockercfg v0.3.2 // indirect
	github.com/creack/pty v1.1.23 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dchest/uniuri v1.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/docker v28.3.3+incompatible // indirect
	github.com/docker/go-connections v0.6.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dprotaso/go-yit v0.0.0-20220510233725-9ba8df137936 // indirect
	github.com/ebitengine/purego v0.8.4 // indirect
	github.com/exaring/otelpgx v0.9.3 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.8 // indirect
	github.com/gaissmai/extnetip v0.3.3 // indirect
	github.com/gin-contrib/sse v1.0.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.26.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/gogo/googleapis v1.4.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/gohugoio/hugo v0.139.4 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/nftables v0.3.0 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/gregjones/httpcache v0.0.0-20180305231024-9cad4c3443a7 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.3.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.2 // indirect
	github.com/hashicorp/cronexpr v1.1.2 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/launchdarkly/ccache v1.1.0 // indirect
	github.com/launchdarkly/eventsource v1.10.0 // indirect
	github.com/launchdarkly/go-jsonstream/v3 v3.1.0 // indirect
	github.com/launchdarkly/go-sdk-events/v3 v3.5.0 // indirect
	github.com/launchdarkly/go-semver v1.0.3 // indirect
	github.com/launchdarkly/go-server-sdk-evaluation/v3 v3.0.1 // indirect
	github.com/launchdarkly/go-server-sdk/v7 v7.13.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/lufia/plan9stats v0.0.0-20240909124753-873cd0166683 // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mdlayher/netlink v1.7.3-0.20250113171957-fbb4dce95f42 // indirect
	github.com/mdlayher/socket v0.5.1 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.1-0.20231216201459-8508981c8b6c // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/go-archive v0.1.0 // indirect
	github.com/moby/patternmatcher v0.6.0 // indirect
	github.com/moby/sys/sequential v0.6.0 // indirect
	github.com/moby/sys/user v0.4.0 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/ngrok/firewall_toolkit v0.0.18 // indirect
	github.com/oapi-codegen/oapi-codegen/v2 v2.4.1 // indirect
	github.com/oasdiff/yaml v0.0.0-20250309154309-f31be36b4037 // indirect
	github.com/oasdiff/yaml3 v0.0.0-20250309153720-d2182401db90 // indirect
	github.com/onsi/ginkgo v1.16.5 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible // indirect
	github.com/paulmach/orb v0.11.1 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/rs/zerolog v1.34.0 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	github.com/shirou/gopsutil/v4 v4.25.6 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/speakeasy-api/openapi-overlay v0.9.0 // indirect
	github.com/spf13/afero v1.11.0 // indirect
	github.com/spf13/cast v1.8.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/tdewolff/parse/v2 v2.7.15 // indirect
	github.com/testcontainers/testcontainers-go v0.39.0 // indirect
	github.com/testcontainers/testcontainers-go/modules/postgres v0.39.0 // indirect
	github.com/tklauser/go-sysconf v0.3.14 // indirect
	github.com/tklauser/numcpus v0.9.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	github.com/vmware-labs/yaml-jsonpath v0.3.2 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.0 // indirect
	go.opentelemetry.io/contrib/bridges/otelzap v0.13.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.63.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.14.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.38.0 // indirect
	go.opentelemetry.io/otel/log v0.14.0 // indirect
	go.opentelemetry.io/otel/sdk v1.38.0 // indirect
	go.opentelemetry.io/otel/sdk/log v0.14.0 // indirect
	go.opentelemetry.io/proto/otlp v1.8.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/arch v0.16.0 // indirect
	golang.org/x/crypto v0.45.0 // indirect
	golang.org/x/exp v0.0.0-20250531010427-b6e5de432a8b // indirect
	golang.org/x/image v0.25.0 // indirect
	golang.org/x/mod v0.29.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	golang.org/x/tools v0.38.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250908214217-97024824d090 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250908214217-97024824d090 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
