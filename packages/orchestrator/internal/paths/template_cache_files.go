package paths

import (
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

type TemplateCacheFiles struct {
	TemplateFiles

	// CacheIdentifier is used to distinguish between each entry in the cache to prevent deleting the cache files when the template cache entry is being closed and a new one is being created.
	CacheIdentifier string
}

func (c TemplateCacheFiles) CacheSnapfilePath() string {
	return filepath.Join(c.cacheDir(), SnapfileName)
}

func (c TemplateCacheFiles) CacheMetadataPath() string {
	return filepath.Join(c.cacheDir(), MetadataName)
}

func (c TemplateCacheFiles) cacheDir() string {
	return filepath.Join(c.config.TemplateCacheDir, c.BuildID, "cache", c.CacheIdentifier)
}

func (c TemplateCacheFiles) NewSandboxFiles(sandboxID string) *SandboxFiles {
	randomID := id.Generate()

	return &SandboxFiles{
		TemplateCacheFiles: c,
		SandboxID:          sandboxID,
		randomID:           randomID,
		tmpDir:             os.TempDir(),
	}
}

func (c TemplateCacheFiles) NewSandboxFilesWithStaticID(sandboxID string, staticID string) *SandboxFiles {
	return &SandboxFiles{
		TemplateCacheFiles: c,
		SandboxID:          sandboxID,
		randomID:           staticID,
		tmpDir:             os.TempDir(),
	}
}
