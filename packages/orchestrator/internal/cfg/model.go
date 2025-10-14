package cfg

import (
	"net"
	"reflect"

	"github.com/caarlos0/env/v11"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
)

type Config struct {
	AllowSandboxInternet       bool     `env:"ALLOW_SANDBOX_INTERNET"       envDefault:"true"`
	ClickhouseConnectionString string   `env:"CLICKHOUSE_CONNECTION_STRING"`
	ForceStop                  bool     `env:"FORCE_STOP"`
	GRPCPort                   uint16   `env:"GRPC_PORT"                    envDefault:"5008"`
	LaunchDarklyAPIKey         string   `env:"LAUNCH_DARKLY_API_KEY"`
	OrchestratorBasePath       string   `env:"ORCHESTRATOR_BASE_PATH"       envDefault:"/orchestrator"`
	OrchestratorLockPath       string   `env:"ORCHESTRATOR_LOCK_PATH"       envDefault:"/orchestrator.lock"`
	ProxyPort                  uint16   `env:"PROXY_PORT"                   envDefault:"5007"`
	RedisClusterURL            string   `env:"REDIS_CLUSTER_URL"`
	RedisURL                   string   `env:"REDIS_URL"`
	Services                   []string `env:"ORCHESTRATOR_SERVICES"        envDefault:"orchestrator"`

	NetworkConfig network.Config
}

func Parse() (Config, error) {
	return env.ParseAsWithOptions[Config](env.Options{
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeOf(net.IPNet{}): network.ParseIPNet,
		},
	})
}
