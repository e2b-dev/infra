package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	EnvsDisk = "/mnt/disks/fc-envs/v1"

	KernelsDir     = "/fc-kernels"
	KernelMountDir = "/fc-vm"
	KernelName     = "vmlinux.bin"

	HostOldEnvdPath  = "/fc-envd/envd-v0.0.1"
	HostEnvdPath     = "/fc-envd/envd"
	GuestOldEnvdPath = "/usr/bin/envd-v0.0.1"
	GuestEnvdPath    = "/usr/bin/envd"

	EnvdVersionKey = "envd_version"
	RootfsSizeKey  = "rootfs_size"

	FirecrackerVersionsDir = "/fc-versions"
	FirecrackerBinaryName  = "firecracker"

	buildDirName = "builds"

	MemfileName  = "memfile"
	RootfsName   = "rootfs.ext4"
	SnapfileName = "snapfile"
)

var BucketName = os.Getenv("TEMPLATE_BUCKET_NAME")

type TemplateFiles struct {
	TemplateId         string
	BuildId            string
	KernelVersion      string
	FirecrackerVersion string
}

func NewTemplateFiles(templateId, buildId, kernelVersion, firecrackerVersion string) *TemplateFiles {
	return &TemplateFiles{
		TemplateId:         templateId,
		BuildId:            buildId,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: firecrackerVersion,
	}
}

func (t *TemplateFiles) BuildKernelPath() string {
	return filepath.Join(t.BuildKernelDir(), KernelName)
}

func (t *TemplateFiles) BuildKernelDir() string {
	return filepath.Join(KernelMountDir, t.KernelVersion)
}

func (t *TemplateFiles) CacheKernelPath() string {
	return filepath.Join(KernelsDir, t.KernelVersion, KernelName)
}

func (t *TemplateFiles) FirecrackerPath() string {
	return filepath.Join(FirecrackerVersionsDir, t.FirecrackerVersion, FirecrackerBinaryName)
}

func (t *TemplateFiles) StorageDir() string {
	return fmt.Sprintf("%s/%s", t.TemplateId, t.BuildId)
}

func (t *TemplateFiles) StorageMemfilePath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), MemfileName)
}

func (t *TemplateFiles) StorageRootfsPath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), RootfsName)
}

func (t *TemplateFiles) StorageSnapfilePath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), SnapfileName)
}

func (t *TemplateFiles) BuildDir() string {
	return filepath.Join(EnvsDisk, t.TemplateId, buildDirName, t.BuildId)
}

func (t *TemplateFiles) BuildMemfilePath() string {
	return filepath.Join(t.BuildDir(), MemfileName)
}

func (t *TemplateFiles) BuildRootfsPath() string {
	return filepath.Join(t.BuildDir(), RootfsName)
}

func (t *TemplateFiles) BuildSnapfilePath() string {
	return filepath.Join(t.BuildDir(), SnapfileName)
}
