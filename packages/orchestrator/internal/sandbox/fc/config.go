package fc

import "path/filepath"

const (
	HostKernelsDir = "/fc-kernels"

	SandboxDir        = "/fc-vm"
	SandboxKernelFile = "vmlinux.bin"

	FirecrackerVersionsDir = "/fc-versions"
	FirecrackerBinaryName  = "firecracker"

	envsDisk     = "/mnt/disks/fc-envs/v1"
	buildDirName = "builds"

	SandboxRootfsFile = "rootfs.ext4"
)

type FirecrackerVersions struct {
	KernelVersion      string
	FirecrackerVersion string
}

func (t FirecrackerVersions) SandboxKernelDir() string {
	return filepath.Join(t.KernelVersion)
}

func (t FirecrackerVersions) HostKernelPath() string {
	return filepath.Join(HostKernelsDir, t.KernelVersion, SandboxKernelFile)
}

func (t FirecrackerVersions) FirecrackerPath() string {
	return filepath.Join(FirecrackerVersionsDir, t.FirecrackerVersion, FirecrackerBinaryName)
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
