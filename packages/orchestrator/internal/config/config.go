package config

import "github.com/e2b-dev/infra/packages/shared/pkg/env"

var (
	AllowSandboxInternet = env.GetEnv("ALLOW_SANDBOX_INTERNET", "true") != "false"
)
