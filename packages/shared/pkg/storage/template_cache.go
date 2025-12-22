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

	config Config
}

func (t TemplateFiles) CacheFiles(config Config) (TemplateCacheFiles, error) {
	identifier, err := uuid.NewRandom()
	if err != nil {
		return TemplateCacheFiles{}, fmt.Errorf("failed to generate identifier: %w", err)
	}

	tcf := TemplateCacheFiles{
		TemplateFiles:   t,
		CacheIdentifier: identifier.String(),
		config:          config,
	}

	cacheDir := tcf.cacheDir()

	err = os.MkdirAll(cacheDir, os.ModePerm)
	if err != nil {
		return TemplateCacheFiles{}, fmt.Errorf("failed to create cache dir '%s': %w", cacheDir, err)
	}

	return tcf, nil
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

func (c TemplateCacheFiles) Close() error {
	return os.RemoveAll(c.cacheDir())
}
