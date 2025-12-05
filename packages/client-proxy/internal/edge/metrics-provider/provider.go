package metrics_provider

import (
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/proxy/internal/cfg"
)

func GetSandboxMetricsQueryProvider(config cfg.Config) (clickhouse.SandboxQueriesProvider, error) {
	if config.ClickhouseConnectionString == "" {
		return clickhouse.NewNoopClient(), nil
	}

	return clickhouse.New(config.ClickhouseConnectionString)
}
