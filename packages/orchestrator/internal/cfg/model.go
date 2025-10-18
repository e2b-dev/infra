package cfg

import (
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
)

type BuilderConfig struct {
	AllowSandboxInternet   bool          `env:"ALLOW_SANDBOX_INTERNET"   envDefault:"true"`
	EnvdTimeout            time.Duration `env:"ENVD_TIMEOUT"             envDefault:"10s"`
	FirecrackerVersionsDir string        `env:"FIRECRACKER_VERSIONS_DIR" envDefault:"/fc-versions"`
	HostKernelsDir         string        `env:"HOST_KERNELS_DIR"         envDefault:"/fc-kernels"`
	OrchestratorBasePath   string        `env:"ORCHESTRATOR_BASE_PATH"   envDefault:"/orchestrator"`
	SandboxDir             string        `env:"SANDBOX_DIR"              envDefault:"/fc-vm"`
	SharedChunkCachePath   string        `env:"SHARED_CHUNK_CACHE_PATH"`

	NetworkConfig network.Config
}

type Config struct {
	BuilderConfig

	ClickhouseConnectionString string   `env:"CLICKHOUSE_CONNECTION_STRING"`
	ForceStop                  bool     `env:"FORCE_STOP"`
	GRPCPort                   uint16   `env:"GRPC_PORT"                    envDefault:"5008"`
	LaunchDarklyAPIKey         string   `env:"LAUNCH_DARKLY_API_KEY"`
	OrchestratorLockPath       string   `env:"ORCHESTRATOR_LOCK_PATH"       envDefault:"/orchestrator.lock"`
	ProxyPort                  uint16   `env:"PROXY_PORT"                   envDefault:"5007"`
	RedisClusterURL            string   `env:"REDIS_CLUSTER_URL"`
	RedisURL                   string   `env:"REDIS_URL"`
	Services                   []string `env:"ORCHESTRATOR_SERVICES"        envDefault:"orchestrator"`
}

func Parse() (Config, error) {
	return env.ParseAs[Config]()
}

func ParseBuilder() (BuilderConfig, error) {
	return env.ParseAs[BuilderConfig]()
}
