package config

import "github.com/e2b-dev/infra/packages/shared/pkg/env"

var (
	AllowSandboxInternet   = env.GetEnv("ALLOW_SANDBOX_INTERNET", "true") != "false"
	WriteClickhouseMetrics = env.GetEnv("WRITE_CLICKHOUSE_METRICS", "false") != "false"
)
