package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

const (
	sandboxCacheDir = "/orchestrator/sandbox"
)

type SandboxFiles struct {
	TemplateCacheFiles
	SandboxID string
	tmpDir    string
	// We use random id to avoid collision between the paused and restored sandbox caches
	randomID string
}

func (c TemplateCacheFiles) NewSandboxFiles(sandboxID string) *SandboxFiles {
	randomID := id.Generate()

	return &SandboxFiles{
		TemplateCacheFiles: c,
		SandboxID:          sandboxID,
		randomID:           randomID,
		tmpDir:             os.TempDir(),
	}
}

func (s *SandboxFiles) SandboxCacheRootfsPath() string {
	return filepath.Join(sandboxCacheDir, fmt.Sprintf("rootfs-%s-%s.cow", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxFirecrackerSocketPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("fc-%s-%s.sock", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxUffdSocketPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("uffd-%s-%s.sock", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxCacheRootfsLinkPath() string {
	return filepath.Join(sandboxCacheDir, fmt.Sprintf("rootfs-%s-%s.link", s.SandboxID, s.randomID))
}
