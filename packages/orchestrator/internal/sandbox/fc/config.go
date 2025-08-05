package fc

import "path/filepath"

const (
	KernelsDir     = "/fc-kernels"
	KernelMountDir = "/fc-vm"
	KernelName     = "vmlinux.bin"

	FirecrackerVersionsDir = "/fc-versions"
	FirecrackerBinaryName  = "firecracker"
)

type FirecrackerVersions struct {
	KernelVersion      string
	FirecrackerVersion string
}

func (t FirecrackerVersions) BuildKernelPath() string {
	return filepath.Join(t.BuildKernelDir(), KernelName)
}

func (t FirecrackerVersions) BuildKernelDir() string {
	return filepath.Join(KernelMountDir, t.KernelVersion)
}

func (t FirecrackerVersions) CacheKernelDir() string {
	return filepath.Join(KernelsDir, t.KernelVersion)
}

func (t FirecrackerVersions) CacheKernelPath() string {
	return filepath.Join(t.CacheKernelDir(), KernelName)
}

func (t FirecrackerVersions) FirecrackerPath() string {
	return filepath.Join(FirecrackerVersionsDir, t.FirecrackerVersion, FirecrackerBinaryName)
}
