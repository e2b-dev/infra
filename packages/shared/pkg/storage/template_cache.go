package storage

import (
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	templateCacheDir = "/orchestrator/template"
)

type TemplateCacheFiles struct {
	*TemplateFiles
	// CacheIdentifier is used to distinguish between each entry in the cache to prevent deleting the cache files when the template cache entry is being closed and a new one is being created.
	CacheIdentifier string
}

func (f *TemplateFiles) NewTemplateCacheFiles() (*TemplateCacheFiles, error) {
	identifier, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate identifier: %w", err)
	}

	return &TemplateCacheFiles{
		TemplateFiles:   f,
		CacheIdentifier: identifier.String(),
	}, nil
}

func (c *TemplateCacheFiles) CacheDir() string {
	return filepath.Join(templateCacheDir, c.TemplateId, c.BuildId, "cache", c.CacheIdentifier)
}

func (c *TemplateCacheFiles) CacheMemfilePath() string {
	return filepath.Join(c.CacheDir(), MemfileName)
}

func (c *TemplateCacheFiles) CacheMemfileDiffPath() string {
	return filepath.Join(c.CacheDir(), MemfileName+".diff")
}

func (c *TemplateCacheFiles) CacheRootfsPath() string {
	return filepath.Join(c.CacheDir(), RootfsName)
}

func (c *TemplateCacheFiles) CacheSnapfilePath() string {
	return filepath.Join(c.CacheDir(), SnapfileName)
}
