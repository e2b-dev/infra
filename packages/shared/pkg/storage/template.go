package storage

import (
	"fmt"
	"path/filepath"
)

const (
	EnvsDisk = "/mnt/disks/fc-envs/v1"

	HostEnvdPath  = "/fc-envd/envd"
	GuestEnvdPath = "/usr/bin/envd"

	buildDirName = "builds"

	MemfileName  = "memfile"
	RootfsName   = "rootfs.ext4"
	SnapfileName = "snapfile"

	HeaderSuffix = ".header"
)

type TemplateFiles struct {
	TemplateID         string `json:"template_id"`
	BuildID            string `json:"build_id"`
	KernelVersion      string `json:"kernel_version"`
	FirecrackerVersion string `json:"firecracker_version"`
}

type RootfsPaths struct {
	TemplateID string
	BuildID    string
}

// Key for the cache. Unique for template-build pair.
func (t TemplateFiles) CacheKey() string {
	return fmt.Sprintf("%s-%s", t.TemplateID, t.BuildID)
}

func (t TemplateFiles) StorageDir() string {
	return t.BuildID
}

func (t TemplateFiles) StorageMemfilePath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), MemfileName)
}

func (t TemplateFiles) StorageMemfileHeaderPath() string {
	return fmt.Sprintf("%s/%s%s", t.StorageDir(), MemfileName, HeaderSuffix)
}

func (t TemplateFiles) StorageRootfsPath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), RootfsName)
}

func (t TemplateFiles) StorageRootfsHeaderPath() string {
	return fmt.Sprintf("%s/%s%s", t.StorageDir(), RootfsName, HeaderSuffix)
}

func (t TemplateFiles) StorageSnapfilePath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), SnapfileName)
}

func (t RootfsPaths) SandboxBuildDir() string {
	return filepath.Join(EnvsDisk, t.TemplateID, buildDirName, t.BuildID)
}

func (t RootfsPaths) SandboxRootfsPath() string {
	return filepath.Join(t.SandboxBuildDir(), RootfsName)
}
