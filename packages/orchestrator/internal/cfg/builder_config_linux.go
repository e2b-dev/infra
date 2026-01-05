//go:build linux

package cfg

import (
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type BuilderConfig struct {
	AllowSandboxInternet   bool          `env:"ALLOW_SANDBOX_INTERNET"   envDefault:"true"`
	EnvdTimeout            time.Duration `env:"ENVD_TIMEOUT"             envDefault:"10s"`
	FirecrackerVersionsDir string        `env:"FIRECRACKER_VERSIONS_DIR" envDefault:"/fc-versions"`
	HostEnvdPath           string        `env:"HOST_ENVD_PATH"           envDefault:"/fc-envd/envd"`
	HostKernelsDir         string        `env:"HOST_KERNELS_DIR"         envDefault:"/fc-kernels"`
	OrchestratorBaseDir    string        `env:"ORCHESTRATOR_BASE_PATH"   envDefault:"/orchestrator"`
	SandboxDir             string        `env:"SANDBOX_DIR"              envDefault:"/fc-vm"`
	SharedChunkCacheDir    string        `env:"SHARED_CHUNK_CACHE_PATH"`
	TemplatesDir           string        `env:"TEMPLATES_DIR,expand"     envDefault:"${ORCHESTRATOR_BASE_PATH}/build-templates"`

	DefaultCacheDir string `env:"DEFAULT_CACHE_DIR,expand"  envDefault:"${ORCHESTRATOR_BASE_PATH}/build"`

	StorageConfig storage.Config
	NetworkConfig network.Config
}
