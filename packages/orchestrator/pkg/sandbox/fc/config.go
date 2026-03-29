package fc

import (
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	SandboxKernelFile = "vmlinux.bin"

	FirecrackerBinaryName = "firecracker"

	// SeccompFilterName is the name of the custom seccomp filter BPF file.
	// On aarch64, the default Firecracker seccomp filter does not include the
	// userfaultfd syscall (nr 282), which is required for UFFD-based snapshot
	// restore. A custom filter that adds userfaultfd can be placed at:
	//   {FirecrackerVersionsDir}/{version}/[{arch}/]seccomp-filter.bpf
	SeccompFilterName = "seccomp-filter.bpf"

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
	}

	return filepath.Join(config.FirecrackerVersionsDir, t.FirecrackerVersion, FirecrackerBinaryName)
}

// SeccompFilterPath returns the path to a custom seccomp filter BPF file if it exists.
// Returns empty string if no custom filter is found. The custom filter should include
// the userfaultfd syscall for UFFD-based snapshot restore on aarch64.
func (t Config) SeccompFilterPath(config cfg.BuilderConfig) string {
	// Check arch-prefixed path first ({version}/{arch}/seccomp-filter.bpf)
	archPath := filepath.Join(config.FirecrackerVersionsDir, t.FirecrackerVersion, utils.TargetArch(), SeccompFilterName)
	if _, err := os.Stat(archPath); err == nil {
		return archPath
	}

	// Fall back to legacy flat path ({version}/seccomp-filter.bpf)
	flatPath := filepath.Join(config.FirecrackerVersionsDir, t.FirecrackerVersion, SeccompFilterName)
	if _, err := os.Stat(flatPath); err == nil {
		return flatPath
	}

	return ""
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
