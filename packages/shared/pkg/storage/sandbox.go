package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	sandboxSocketDir = "/sandbox"
)

type SandboxFiles struct {
	*TemplateFiles
	SandboxID string
}

func NewSandboxFiles(templateFiles *TemplateFiles, sandboxID string) *SandboxFiles {
	return &SandboxFiles{
		TemplateFiles: templateFiles,
		SandboxID:     sandboxID,
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
