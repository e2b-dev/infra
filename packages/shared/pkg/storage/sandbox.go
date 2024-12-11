package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

type SandboxFiles struct {
	*TemplateCacheFiles
	SandboxID string
	tmpDir    string
	randomID  string
}

func (c *TemplateCacheFiles) NewSandboxFiles(sandboxID string) *SandboxFiles {
	randomID := id.Generate()

	return &SandboxFiles{
		TemplateCacheFiles: c,
		SandboxID:          sandboxID,
		randomID:           randomID,
		tmpDir:             os.TempDir(),
	}
}

func (s *SandboxFiles) SandboxCacheDir() string {
	return filepath.Join(s.CacheDir(), "sandbox", s.SandboxID, s.randomID)
}

func (s *SandboxFiles) SandboxCacheRootfsPath() string {
	return filepath.Join(s.SandboxCacheDir(), RootfsName)
}

func (s *SandboxFiles) SandboxFirecrackerSocketPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("fc-%s-%s.sock", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxUffdSocketPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("uffd-%s-%s.sock", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxCacheRootfsLinkPath() string {
	return filepath.Join(s.SandboxCacheDir(), "rootfs.link")
}
