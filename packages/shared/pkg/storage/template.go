package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	EnvsDisk        = "/mnt/disks/fc-envs/v1"
	LocalStorageDir = "/tmp"

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

	HeaderSuffix = ".header"
)

// Path to the directory where the kernel can be accessed inside when the dirs are mounted.
var KernelMountedPath = filepath.Join(KernelMountDir, KernelName)

type Type string

const (
	LocalStorage  Type = "local"
	BucketStorage Type = "bucket"
)

type TemplateFiles struct {
	TemplateId         string
	BuildId            string
	KernelVersion      string
	FirecrackerVersion string

	hugePages   bool
	StorageType Type
}

func NewTemplateFiles(
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) *TemplateFiles {
	// Choose where are the template build data stored. Default to bucket storage.
	var storageType Type
	switch os.Getenv("TEMPLATE_STORAGE") {
	case "local":
		storageType = LocalStorage
	default:
		storageType = BucketStorage
	}

	return &TemplateFiles{
		TemplateId:         templateId,
		BuildId:            buildId,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: firecrackerVersion,
		hugePages:          hugePages,
		StorageType:        storageType,
	}
}

func (t *TemplateFiles) BuildKernelPath() string {
	return filepath.Join(t.BuildKernelDir(), KernelName)
}

func (t *TemplateFiles) BuildKernelDir() string {
	return filepath.Join(KernelMountDir, t.KernelVersion)
}

// Key for the cache. Unique for template-build pair.
func (t *TemplateFiles) CacheKey() string {
	return fmt.Sprintf("%s-%s", t.TemplateId, t.BuildId)
}

func (t *TemplateFiles) CacheKernelDir() string {
	return filepath.Join(KernelsDir, t.KernelVersion)
}

func (t *TemplateFiles) CacheKernelPath() string {
	return filepath.Join(t.CacheKernelDir(), KernelName)
}

func (t *TemplateFiles) FirecrackerPath() string {
	return filepath.Join(FirecrackerVersionsDir, t.FirecrackerVersion, FirecrackerBinaryName)
}

func (t *TemplateFiles) StorageDir() string {
	return t.BuildId
}

func (t *TemplateFiles) StorageMemfilePath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), MemfileName)
}

func (t *TemplateFiles) StorageMemfileHeaderPath() string {
	return fmt.Sprintf("%s/%s%s", t.StorageDir(), MemfileName, HeaderSuffix)
}

func (t *TemplateFiles) StorageRootfsPath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), RootfsName)
}

func (t *TemplateFiles) StorageRootfsHeaderPath() string {
	return fmt.Sprintf("%s/%s%s", t.StorageDir(), RootfsName, HeaderSuffix)
}

func (t *TemplateFiles) StorageSnapfilePath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), SnapfileName)
}

func (t *TemplateFiles) BuildDir() string {
	var baseDir string
	switch t.StorageType {
	case LocalStorage:
		baseDir = LocalStorageDir
	default:
		baseDir = EnvsDisk
	}

	return filepath.Join(baseDir, t.TemplateId, buildDirName, t.BuildId)
}

func (t *TemplateFiles) BuildMemfilePath() string {
	return filepath.Join(t.BuildDir(), MemfileName)
}

func (t *TemplateFiles) BuildRootfsPath() string {
	return filepath.Join(t.BuildDir(), RootfsName)
}

func (t *TemplateFiles) BuildMemfileDiffPath() string {
	return filepath.Join(t.BuildDir(), fmt.Sprintf("%s.diff", MemfileName))
}

func (t *TemplateFiles) BuildRootfsDiffPath() string {
	return filepath.Join(t.BuildDir(), fmt.Sprintf("%s.diff", RootfsName))
}

func (t *TemplateFiles) BuildSnapfilePath() string {
	return filepath.Join(t.BuildDir(), SnapfileName)
}

func (t *TemplateFiles) Hugepages() bool {
	return t.hugePages
}

func (t *TemplateFiles) MemfilePageSize() int64 {
	if t.hugePages {
		return header.HugepageSize
	}

	return header.PageSize
}

func (t *TemplateFiles) RootfsBlockSize() int64 {
	return header.RootfsBlockSize
}
