package metrics_provider

import (
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/proxy/internal/cfg"
)

func GetSandboxMetricsQueryProvider(config cfg.Config) (clickhouse.SandboxQueriesProvider, error) {
	return clickhouse.New(config.ClickhouseConnectionString)
}
