package template

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	EnvsDisk         = "/mnt/disks/fc-envs/v1"
	templateCacheDir = "/template/cache"

	KernelsDir     = "/fc-kernels"
	KernelMountDir = "/fc-vm"
	KernelName     = "vmlinux.bin"

	HostOldEnvdPath  = "/fc-vm/envd-v0.0.1"
	HostEnvdPath     = "/fc-vm/envd"
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
	TemplateID string
	BuildID    string
}

func NewTemplateFiles(templateID, buildID string) *TemplateFiles {
	return &TemplateFiles{
		TemplateID: templateID,
		BuildID:    buildID,
	}
}

func (t *TemplateFiles) StorageDir() string {
	return fmt.Sprintf("%s/%s", t.TemplateID, t.BuildID)
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
	return filepath.Join(EnvsDisk, t.TemplateID, buildDirName, t.BuildID)
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

func (t *TemplateFiles) CacheDir() string {
	return filepath.Join(templateCacheDir, t.TemplateID, t.BuildID)
}

func (t *TemplateFiles) CacheMemfilePath() string {
	return filepath.Join(t.CacheDir(), MemfileName)
}

func (t *TemplateFiles) CacheRootfsPath() string {
	return filepath.Join(t.CacheDir(), RootfsName)
}

func (t *TemplateFiles) CacheSnapfilePath() string {
	return filepath.Join(t.CacheDir(), SnapfileName)
}