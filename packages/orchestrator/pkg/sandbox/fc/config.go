package fc

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
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
	// Prefer arch-prefixed path ({version}/{arch}/vmlinux.bin) for multi-arch support.
	// Fall back to legacy flat path ({version}/vmlinux.bin) for existing production nodes.
	archPath := filepath.Join(config.HostKernelsDir, t.KernelVersion, utils.TargetArch(), SandboxKernelFile)
	if _, err := os.Stat(archPath); err == nil {
		return archPath
	} else if !errors.Is(err, os.ErrNotExist) {
		// Non-existence errors (e.g. permission denied) should not silently fall back
		// to the legacy path, as that could use the wrong binary.
		return archPath
	}

	return filepath.Join(config.HostKernelsDir, t.KernelVersion, SandboxKernelFile)
}

func (t Config) FirecrackerPath(config cfg.BuilderConfig) string {
	// Prefer arch-prefixed path ({version}/{arch}/firecracker) for multi-arch support.
	// Fall back to legacy flat path ({version}/firecracker) for existing production nodes
	// that haven't migrated to the arch-prefixed layout yet.
	archPath := filepath.Join(config.FirecrackerVersionsDir, t.FirecrackerVersion, utils.TargetArch(), FirecrackerBinaryName)
	if _, err := os.Stat(archPath); err == nil {
		return archPath
	} else if !errors.Is(err, os.ErrNotExist) {
		// Non-existence errors (e.g. permission denied) should not silently fall back
		// to the legacy path, as that could use the wrong binary.
		return archPath
	}

	return filepath.Join(config.FirecrackerVersionsDir, t.FirecrackerVersion, FirecrackerBinaryName)
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
