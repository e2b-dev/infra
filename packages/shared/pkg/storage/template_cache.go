package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

type TemplateCacheFiles struct {
	TemplateFiles

	// CacheIdentifier is used to distinguish between each entry in the cache to prevent deleting the cache files when the template cache entry is being closed and a new one is being created.
	CacheIdentifier string
}

func (t TemplateFiles) CacheFiles(config BuilderConfig) (TemplateCacheFiles, error) {
	identifier, err := uuid.NewRandom()
	if err != nil {
		return TemplateCacheFiles{}, fmt.Errorf("failed to generate identifier: %w", err)
	}

	tcf := TemplateCacheFiles{
		TemplateFiles:   t,
		CacheIdentifier: identifier.String(),
	}

	cacheDir := tcf.cacheDir(config)

	err = os.MkdirAll(cacheDir, os.ModePerm)
	if err != nil {
		return TemplateCacheFiles{}, fmt.Errorf("failed to create cache dir '%s': %w", cacheDir, err)
	}

	return tcf, nil
}

func (c TemplateCacheFiles) CacheSnapfilePath(config BuilderConfig) string {
	return filepath.Join(c.cacheDir(config), SnapfileName)
}

func (c TemplateCacheFiles) CacheMetadataPath(config BuilderConfig) string {
	return filepath.Join(c.cacheDir(config), MetadataName)
}

func (c TemplateCacheFiles) cacheDir(config BuilderConfig) string {
	return filepath.Join(config.GetTemplateCacheDir(), c.BuildID, "cache", c.CacheIdentifier)
}
