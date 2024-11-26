package storage

import (
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	templateCacheDir = "/orchestrator/template"
)

// Ensure that when the template is being closed we are not adding it to the cache so the newly created cache files are not deleted.
// We can improve this by either locking the key or by saving the template data under a prefixed direcotry for each entry.
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

func (c *TemplateCacheFiles) CacheRootfsPath() string {
	return filepath.Join(c.CacheDir(), RootfsName)
}

func (c *TemplateCacheFiles) CacheSnapfilePath() string {
	return filepath.Join(c.CacheDir(), SnapfileName)
}
