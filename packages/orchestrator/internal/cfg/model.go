package cfg

import (
	"fmt"
	"path/filepath"

	"github.com/caarlos0/env/v11"
)

func (bc BuilderConfig) GetSandboxCacheDir() string  { return bc.SandboxCacheDir }
func (bc BuilderConfig) GetSnapshotCacheDir() string { return bc.SnapshotCacheDir }
func (bc BuilderConfig) GetTemplateCacheDir() string { return bc.TemplateCacheDir }

func makePathsAbsolute(c *BuilderConfig) error {
	for _, item := range []*string{
		&c.DefaultCacheDir,
		&c.FirecrackerVersionsDir,
		&c.HostEnvdPath,
		&c.HostKernelsDir,
		&c.OrchestratorBaseDir,
		&c.SandboxCacheDir,
		&c.SandboxDir,
		&c.SharedChunkCacheDir,
		&c.SnapshotCacheDir,
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
			return fmt.Errorf("failed to resolve %q to absolute path: %w", *item, err)
		}

		*item = dir
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
	RedisTLSCABase64           string   `env:"REDIS_TLS_CA_BASE64"`
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
