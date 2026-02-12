package fc

import (
	"path/filepath"
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	SandboxKernelFile = "vmlinux.bin"

	FirecrackerBinaryName = "firecracker"

	envsDisk     = "/mnt/disks/fc-envs/v1"
	buildDirName = "builds"

	SandboxRootfsFile = "rootfs.ext4"

	entropyBytesSize    int64 = 1024 // 1 KB
	entropyRefillTime   int64 = 100
	entropyOneTimeBurst int64 = 0
)

type Config struct {
	KernelVersion      string
	FirecrackerVersion string
}

func (t Config) SandboxKernelDir() string {
	return t.KernelVersion
}

func (t Config) HostKernelPath(config cfg.BuilderConfig) string {
	return filepath.Join(config.HostKernelsDir, t.KernelVersion, SandboxKernelFile)
}

func (t Config) FirecrackerPath(config cfg.BuilderConfig) string {
	return filepath.Join(config.FirecrackerVersionsDir, t.FirecrackerVersion, FirecrackerBinaryName)
}

// SupportsFreePageReporting reports whether the Firecracker version supports
// free page reporting via the balloon device. Free page reporting was introduced
// in Firecracker v1.14.0.
func (t Config) SupportsFreePageReporting() bool {
	// Version format is vX.Y.Z_commithash — strip the commit hash before comparing.
	versionOnly, _, _ := strings.Cut(t.FirecrackerVersion, "_")

	supported, err := utils.IsGTEVersion(versionOnly, "v1.14.0")
	if err != nil {
		return false
	}

	return supported
}

type RootfsPaths struct {
	TemplateVersion uint64
	TemplateID      string
	BuildID         string
}

var ConstantRootfsPaths = RootfsPaths{
	// The version is always 2 for the constant rootfs paths format change.
	TemplateVersion: 2,
}

// Deprecated: Use static rootfs path instead.
func (t RootfsPaths) DeprecatedSandboxRootfsDir() string {
	return filepath.Join(envsDisk, t.TemplateID, buildDirName, t.BuildID)
}
