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

func (c *TemplateCacheFiles) NewSandboxFiles(sandboxID string) *SandboxFiles {
	return &SandboxFiles{
		TemplateCacheFiles: c,
		SandboxID:          sandboxID,
	}
}

func (s *SandboxFiles) SandboxCacheDir() string {
	return filepath.Join(s.CacheDir(), "sandbox", s.SandboxID)
}

func (s *SandboxFiles) SandboxCacheRootfsPath() string {
	return filepath.Join(s.SandboxCacheDir(), RootfsName)
}

func (s *SandboxFiles) SandboxFirecrackerSocketPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("fc-%s.sock", s.SandboxID))
}

func (s *SandboxFiles) SandboxUffdSocketPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("uffd-%s.sock", s.SandboxID))
}
