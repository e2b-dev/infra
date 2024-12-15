package storage

import (
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	templateCacheDir = "/orchestrator/template"
	snapshotCacheDir = "/mnt/snapshot-cache"
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

func (c *TemplateCacheFiles) CacheMemfileFullSnapshotPath() string {
	name := fmt.Sprintf("%s-%s-%s.full", c.BuildId, MemfileName, c.CacheIdentifier)

	return filepath.Join(snapshotCacheDir, name)
}

func (c *TemplateCacheFiles) CacheSnapfilePath() string {
	return filepath.Join(c.CacheDir(), SnapfileName)
}
