package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	templateCacheDir = "/orchestrator/template"
)

type TemplateCacheFiles struct {
	TemplateFiles
	// CacheIdentifier is used to distinguish between each entry in the cache to prevent deleting the cache files when the template cache entry is being closed and a new one is being created.
	CacheIdentifier string
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

func (c TemplateCacheFiles) CacheSnapfilePath() string {
	return filepath.Join(c.cacheDir(), SnapfileName)
}

func (c TemplateCacheFiles) cacheDir() string {
	return filepath.Join(templateCacheDir, c.BuildID, "cache", c.CacheIdentifier)
}
