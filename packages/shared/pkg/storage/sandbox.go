package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

type SandboxFiles struct {
	*TemplateCacheFiles
	SandboxID string
}

func NewSandboxFiles(templateCacheFiles *TemplateCacheFiles, sandboxID string) *SandboxFiles {
	return &SandboxFiles{
		TemplateCacheFiles: templateCacheFiles,
		SandboxID:          sandboxID,
	}
}

func (t *SandboxFiles) SandboxCacheDir() string {
	return filepath.Join(t.CacheDir(), "sandbox", t.SandboxID)
}

func (t *SandboxFiles) SandboxCacheRootfsPath() string {
	return filepath.Join(t.SandboxCacheDir(), RootfsName)
}

func (t *SandboxFiles) SandboxFirecrackerSocketPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("fc-%s.sock", t.SandboxID))
}

func (t *SandboxFiles) SandboxUffdSocketPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("uffd-%s.sock", t.SandboxID))
}
