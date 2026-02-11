package fc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestFirecrackerPath_ArchPrefixed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := utils.TargetArch()

	// Create the arch-prefixed binary
	archDir := filepath.Join(dir, "v1.12.0", arch)
	require.NoError(t, os.MkdirAll(archDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(archDir, "firecracker"), []byte("binary"), 0o755))

	config := cfg.BuilderConfig{FirecrackerVersionsDir: dir}
	fc := Config{FirecrackerVersion: "v1.12.0"}

	result := fc.FirecrackerPath(config)

	assert.Equal(t, filepath.Join(dir, "v1.12.0", arch, "firecracker"), result)
}

func TestFirecrackerPath_LegacyFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Only create the legacy flat binary (no arch subdirectory)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "v1.12.0"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.12.0", "firecracker"), []byte("binary"), 0o755))

	config := cfg.BuilderConfig{FirecrackerVersionsDir: dir}
	fc := Config{FirecrackerVersion: "v1.12.0"}

	result := fc.FirecrackerPath(config)

	assert.Equal(t, filepath.Join(dir, "v1.12.0", "firecracker"), result)
}

func TestFirecrackerPath_NeitherExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// No binary at all — should return legacy flat path
	config := cfg.BuilderConfig{FirecrackerVersionsDir: dir}
	fc := Config{FirecrackerVersion: "v1.12.0"}

	result := fc.FirecrackerPath(config)

	assert.Equal(t, filepath.Join(dir, "v1.12.0", "firecracker"), result)
}

func TestHostKernelPath_ArchPrefixed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := utils.TargetArch()

	// Create the arch-prefixed kernel
	archDir := filepath.Join(dir, "vmlinux-6.1.102", arch)
	require.NoError(t, os.MkdirAll(archDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(archDir, "vmlinux.bin"), []byte("kernel"), 0o644))

	config := cfg.BuilderConfig{HostKernelsDir: dir}
	fc := Config{KernelVersion: "vmlinux-6.1.102"}

	result := fc.HostKernelPath(config)

	assert.Equal(t, filepath.Join(dir, "vmlinux-6.1.102", arch, "vmlinux.bin"), result)
}

func TestHostKernelPath_LegacyFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Only create the legacy flat kernel
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vmlinux-6.1.102"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vmlinux-6.1.102", "vmlinux.bin"), []byte("kernel"), 0o644))

	config := cfg.BuilderConfig{HostKernelsDir: dir}
	fc := Config{KernelVersion: "vmlinux-6.1.102"}

	result := fc.HostKernelPath(config)

	assert.Equal(t, filepath.Join(dir, "vmlinux-6.1.102", "vmlinux.bin"), result)
}

func TestHostKernelPath_PrefersArchOverLegacy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := utils.TargetArch()

	// Create BOTH arch-prefixed and legacy flat kernels
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vmlinux-6.1.102", arch), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vmlinux-6.1.102", arch, "vmlinux.bin"), []byte("arch-kernel"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vmlinux-6.1.102", "vmlinux.bin"), []byte("legacy-kernel"), 0o644))

	config := cfg.BuilderConfig{HostKernelsDir: dir}
	fc := Config{KernelVersion: "vmlinux-6.1.102"}

	result := fc.HostKernelPath(config)

	// Should prefer the arch-prefixed path
	assert.Equal(t, filepath.Join(dir, "vmlinux-6.1.102", arch, "vmlinux.bin"), result)
}
