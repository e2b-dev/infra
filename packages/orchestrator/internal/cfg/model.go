package cfg

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
)

type BuilderConfig struct {
	AllowSandboxInternet   bool          `env:"ALLOW_SANDBOX_INTERNET"    envDefault:"true"`
	DefaultCachePath       string        `env:"DEFAULT_CACHE_PATH,expand" envDefault:"${ORCHESTRATOR_BASE_PATH}/build"`
	EnvdTimeout            time.Duration `env:"ENVD_TIMEOUT"              envDefault:"10s"`
	FirecrackerVersionsDir string        `env:"FIRECRACKER_VERSIONS_DIR"  envDefault:"/fc-versions"`
	HostEnvdPath           string        `env:"HOST_ENVD_PATH"            envDefault:"/fc-envd/envd"`
	HostKernelsDir         string        `env:"HOST_KERNELS_DIR"          envDefault:"/fc-kernels"`
	OrchestratorBasePath   string        `env:"ORCHESTRATOR_BASE_PATH"    envDefault:"/orchestrator"`
	SandboxCacheDir        string        `env:"SANDBOX_CACHE_DIR,expand"  envDefault:"${ORCHESTRATOR_BASE_PATH}/sandbox"`
	SandboxDir             string        `env:"SANDBOX_DIR"               envDefault:"/fc-vm"`
	SharedChunkCachePath   string        `env:"SHARED_CHUNK_CACHE_PATH"`
	TemplateCacheDir       string        `env:"TEMPLATE_CACHE_DIR,expand" envDefault:"${ORCHESTRATOR_BASE_PATH}/template"`
	TemplatesDir           string        `env:"TEMPLATES_DIR,expand"      envDefault:"${ORCHESTRATOR_BASE_PATH}/build-templates"`

	NetworkConfig network.Config
}

func makePathsAbsolute(c *BuilderConfig) error {
	for _, item := range []*string{
		&c.DefaultCachePath,
		&c.FirecrackerVersionsDir,
		&c.HostEnvdPath,
		&c.HostKernelsDir,
		&c.OrchestratorBasePath,
		&c.SandboxCacheDir,
		&c.SandboxDir,
		&c.SharedChunkCachePath,
		&c.TemplateCacheDir,
		&c.TemplatesDir,
	} {
		dir := *item

		if dir == "" {
			continue
		}

		if filepath.IsAbs(dir) {
			continue
		}

		dir, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("failed to absolutify %q: %w", *item, err)
		}

		*item = dir
	}

	for _, dir := range []string{
		c.OrchestratorBasePath,
		c.DefaultCachePath,
		c.SandboxCacheDir,
		c.SandboxDir,
		c.SharedChunkCachePath,
		c.TemplateCacheDir,
		c.TemplatesDir,
	} {
		if dir == "" {
			continue
		}

		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("failed to make %q: %w", dir, err)
		}

	}

	return nil
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
	config, err := env.ParseAs[Config]()
	if err != nil {
		return config, err
	}

	bc := config.BuilderConfig
	if err = makePathsAbsolute(&bc); err != nil {
		return config, err
	}

	config.BuilderConfig = bc

	return config, nil
}

func ParseBuilder() (BuilderConfig, error) {
	model, err := env.ParseAs[BuilderConfig]()
	if err != nil {
		return BuilderConfig{}, err
	}

	if err = makePathsAbsolute(&model); err != nil {
		return BuilderConfig{}, err
	}

	return model, nil
}
