package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

type SandboxFiles struct {
	*TemplateCacheFiles
	SandboxID string
	tmpDir    string
}

func (c *TemplateCacheFiles) NewSandboxFiles(sandboxID string) *SandboxFiles {
	return &SandboxFiles{
		TemplateCacheFiles: c,
		SandboxID:          sandboxID,
		tmpDir:             os.TempDir(),
	}
}

func (s *SandboxFiles) SandboxCacheDir() string {
	return filepath.Join(s.CacheDir(), "sandbox", s.SandboxID)
}

func (s *SandboxFiles) SandboxCacheRootfsPath() string {
	return filepath.Join(s.SandboxCacheDir(), RootfsName)
}

func (s *SandboxFiles) SandboxFirecrackerSocketPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("fc-%s.sock", s.SandboxID))
}

func (s *SandboxFiles) SandboxUffdSocketPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("uffd-%s.sock", s.SandboxID))
}

func (s *SandboxFiles) SandboxCacheRootfsLinkPath() string {
	return filepath.Join(s.SandboxCacheDir(), "rootfs.link")
}
