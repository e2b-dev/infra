package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

type SandboxFiles struct {
	CachePaths

	SandboxID string
	tmpDir    string
	// We use random id to avoid collision between the paused and restored sandbox caches
	randomID string
}

type Config struct {
	CompressConfig

	SandboxCacheDir  string `env:"SANDBOX_CACHE_DIR,expand"  envDefault:"${ORCHESTRATOR_BASE_PATH}/sandbox"`
	SnapshotCacheDir string `env:"SNAPSHOT_CACHE_DIR,expand" envDefault:"/mnt/snapshot-cache"`
	TemplateCacheDir string `env:"TEMPLATE_CACHE_DIR,expand" envDefault:"${ORCHESTRATOR_BASE_PATH}/template"`
}

func (c CachePaths) NewSandboxFiles(sandboxID string) *SandboxFiles {
	randomID := id.Generate()

	return &SandboxFiles{
		CachePaths: c,
		SandboxID:  sandboxID,
		randomID:   randomID,
		tmpDir:     os.TempDir(),
	}
}

func (c CachePaths) NewSandboxFilesWithStaticID(sandboxID string, staticID string) *SandboxFiles {
	return &SandboxFiles{
		CachePaths: c,
		SandboxID:  sandboxID,
		randomID:   staticID,
		tmpDir:     os.TempDir(),
	}
}

func (s *SandboxFiles) SandboxCacheRootfsPath(config Config) string {
	return filepath.Join(config.SandboxCacheDir, fmt.Sprintf("rootfs-%s-%s.cow", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxFirecrackerSocketPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("fc-%s-%s.sock", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxUffdSocketPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("uffd-%s-%s.sock", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxCacheRootfsLinkPath(config Config) string {
	return filepath.Join(config.SandboxCacheDir, fmt.Sprintf("rootfs-%s-%s.link", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxMetricsFifoPath() string {
	return filepath.Join(s.tmpDir, fmt.Sprintf("fc-metrics-%s-%s.fifo", s.SandboxID, s.randomID))
}

func (s *SandboxFiles) SandboxCgroupName() string {
	return fmt.Sprintf("sbx-%s-%s", s.SandboxID, s.randomID)
}

// SandboxFileGlobs returns glob patterns matching the on-disk files a sandbox
// creates: firecracker/uffd sockets and the metrics fifo under tempDir, plus
// the rootfs overlay and link files under sandboxCacheDir. The patterns mirror
// the path builders above and are the single source of truth used by startup
// reclaim. sandboxCacheDir may be empty, in which case the cache patterns are
// omitted.
func SandboxFileGlobs(tempDir, sandboxCacheDir string) []string {
	patterns := []string{
		filepath.Join(tempDir, "fc-*-*.sock"),
		filepath.Join(tempDir, "uffd-*-*.sock"),
		filepath.Join(tempDir, "fc-metrics-*-*.fifo"),
	}
	if sandboxCacheDir != "" {
		patterns = append(patterns,
			filepath.Join(sandboxCacheDir, "rootfs-*-*.cow"),
			filepath.Join(sandboxCacheDir, "rootfs-*-*.link"),
		)
	}

	return patterns
}

// ReclaimSandboxFiles removes leaked sandbox files matching SandboxFileGlobs,
// left over from sandboxes that did not shut down cleanly. It returns the number
// of files removed and any per-file removal failures. Files that no longer exist
// are treated as already reclaimed.
func ReclaimSandboxFiles(tempDir, sandboxCacheDir string) (int, []error) {
	paths, err := matchingSandboxFiles(tempDir, sandboxCacheDir)
	if err != nil {
		return 0, []error{err}
	}

	reclaimed := 0
	var failures []error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			failures = append(failures, fmt.Errorf("failed to remove %s: %w", path, err))

			continue
		}

		reclaimed++
	}

	return reclaimed, failures
}

func matchingSandboxFiles(tempDir, sandboxCacheDir string) ([]string, error) {
	paths := make([]string, 0)
	for _, pattern := range SandboxFileGlobs(tempDir, sandboxCacheDir) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to glob %s: %w", pattern, err)
		}

		paths = append(paths, matches...)
	}

	slices.Sort(paths)

	return paths, nil
}
