package cfg

import (
	"github.com/caarlos0/env/v11"
)

type Config struct {
	AllowSandboxInternet       bool     `env:"ALLOW_SANDBOX_INTERNET"       envDefault:"true"`
	ClickhouseConnectionString string   `env:"CLICKHOUSE_CONNECTION_STRING"`
	ForceStop                  bool     `env:"FORCE_STOP"`
	GRPCPort                   uint16   `env:"GRPC_PORT"                    envDefault:"5008"`
	HyperloopIPAddress         string   `env:"SANDBOX_HYPERLOOP_IP"         envDefault:"192.0.2.1"`
	HyperloopProxyPort         uint16   `env:"SANDBOX_HYPERLOOP_PROXY_PORT" envDefault:"5010"`
	LaunchDarklyAPIKey         string   `env:"LAUNCH_DARKLY_API_KEY"`
	OrchestratorBasePath       string   `env:"ORCHESTRATOR_BASE_PATH"       envDefault:"/orchestrator"`
	OrchestratorLockPath       string   `env:"ORCHESTRATOR_LOCK_PATH"       envDefault:"/orchestrator.lock"`
	ProxyPort                  uint16   `env:"PROXY_PORT"                   envDefault:"5007"`
	RedisClusterURL            string   `env:"REDIS_CLUSTER_URL"`
	RedisURL                   string   `env:"REDIS_URL"`
	Services                   []string `env:"ORCHESTRATOR_SERVICES"        envDefault:"orchestrator"`
	UseLocalNamespaceStorage   bool     `env:"USE_LOCAL_NAMESPACE_STORAGE"`
}

func Parse() (Config, error) {
	var model Config
	err := env.Parse(&model)
	return model, err
}
