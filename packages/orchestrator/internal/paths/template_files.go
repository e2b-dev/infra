package paths

import (
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
)

const (
	GuestEnvdPath = "/usr/bin/envd"

	MemfileName  = "memfile"
	RootfsName   = "rootfs.ext4"
	SnapfileName = "snapfile"
	MetadataName = "metadata.json"

	HeaderSuffix = ".header"
)

func HostEnvdPath() string {
	if value := os.Getenv("HOST_ENVD_PATH"); value != "" {
		return value
	}

	return "/fc-envd/envd"
}

type TemplateFiles struct {
	config cfg.BuilderConfig

	BuildID            string `json:"build_id"`
	KernelVersion      string `json:"kernel_version"`
	FirecrackerVersion string `json:"firecracker_version"`
}

func New(config cfg.BuilderConfig, buildID string) TemplateFiles {
	return TemplateFiles{
		config:  config,
		BuildID: buildID,
	}
}

func NewWithVersions(config cfg.BuilderConfig, buildID, kernelVersion, firecrackerVersion string) TemplateFiles {
	return TemplateFiles{
		config:             config,
		BuildID:            buildID,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: firecrackerVersion,
	}
}

// CacheKey returns the key for the cache. Unique for template-build pair.
func (t TemplateFiles) CacheKey() string {
	return t.BuildID
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

func (t TemplateFiles) StorageMetadataPath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), MetadataName)
}

func (t TemplateFiles) CacheFiles() (TemplateCacheFiles, error) {
	identifier, err := uuid.NewRandom()
	if err != nil {
		return TemplateCacheFiles{}, fmt.Errorf("failed to generate identifier: %w", err)
	}

	tcf := TemplateCacheFiles{
		TemplateFiles:   t,
		CacheIdentifier: identifier.String(),
	}

	err = os.MkdirAll(tcf.cacheDir(), os.ModePerm)
	if err != nil {
		return TemplateCacheFiles{}, fmt.Errorf("failed to create cache dir '%s': %w", tcf.cacheDir(), err)
	}

	return tcf, nil
}
