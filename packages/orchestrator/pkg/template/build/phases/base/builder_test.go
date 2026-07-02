package base
//go:build linux

package base

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupFakeRootfs creates a minimal fake rootfs directory tree that simulates
// a mounted ext4 image. It returns the path to the fake mount point.
func setupFakeRootfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Create the directory structure that a real rootfs would have.
	dirs := []string{
		"etc/systemd/system",
		"etc/systemd/system-preset",
		"usr/lib/systemd/system",
		"usr/lib/systemd/system-preset",
	}
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}

	return root
}

// createFakeServiceFile creates a dummy unit file at the given path inside root.
func createFakeServiceFile(t *testing.T, root, relPath string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte("[Unit]\nDescription=fake\n"), 0o644))
}

// readSymlink reads the target of a symlink and returns it.
func readSymlink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	require.NoError(t, err, "expected a symlink at %s", path)
	return target
}

// ─── preset file content ──────────────────────────────────────────────────────

func TestApplyEnvdPresetFiles_EtcPresetFileContent(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)

	require.NoError(t, applyEnvdPresetFiles(root))

	content, err := os.ReadFile(filepath.Join(root, "etc/systemd/system-preset/80-envd.preset"))
	require.NoError(t, err)
	assert.Equal(t, "enable envd.service\n", string(content))
}

func TestApplyEnvdPresetFiles_UsrLibPresetFileContent(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)

	require.NoError(t, applyEnvdPresetFiles(root))

	content, err := os.ReadFile(filepath.Join(root, "usr/lib/systemd/system-preset/80-envd.preset"))
	require.NoError(t, err)
	assert.Equal(t, "enable envd.service\n", string(content))
}

// ─── envd.service symlink ─────────────────────────────────────────────────────

func TestApplyEnvdPresetFiles_EnvdSymlinkDefaultsToEtcSystemd(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)
	// Place envd.service in /etc/systemd/system/ (Debian/Ubuntu layout).
	createFakeServiceFile(t, root, "etc/systemd/system/envd.service")

	require.NoError(t, applyEnvdPresetFiles(root))

	symlink := filepath.Join(root, "etc/systemd/system/multi-user.target.wants/envd.service")
	target := readSymlink(t, symlink)
	assert.Equal(t, "/etc/systemd/system/envd.service", target)
}

func TestApplyEnvdPresetFiles_EnvdSymlinkFallsBackToUsrLib(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)
	// Only place envd.service in /usr/lib/systemd/system/ (RHEL/CentOS layout).
	createFakeServiceFile(t, root, "usr/lib/systemd/system/envd.service")

	require.NoError(t, applyEnvdPresetFiles(root))

	symlink := filepath.Join(root, "etc/systemd/system/multi-user.target.wants/envd.service")
	target := readSymlink(t, symlink)
	assert.Equal(t, "/usr/lib/systemd/system/envd.service", target)
}

func TestApplyEnvdPresetFiles_EnvdSymlinkReplacesExistingBrokenSymlink(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)
	createFakeServiceFile(t, root, "etc/systemd/system/envd.service")

	// Pre-create a broken symlink.
	wantsDir := filepath.Join(root, "etc/systemd/system/multi-user.target.wants")
	require.NoError(t, os.MkdirAll(wantsDir, 0o755))
	brokenSymlink := filepath.Join(wantsDir, "envd.service")
	require.NoError(t, os.Symlink("/nonexistent/path", brokenSymlink))

	require.NoError(t, applyEnvdPresetFiles(root))

	target := readSymlink(t, brokenSymlink)
	assert.Equal(t, "/etc/systemd/system/envd.service", target,
		"broken symlink should be replaced with the correct target")
}

func TestApplyEnvdPresetFiles_WantsDirCreatedIfMissing(t *testing.T) {
	t.Parallel()
	root := t.TempDir() // intentionally empty — no pre-created dirs

	require.NoError(t, applyEnvdPresetFiles(root))

	wantsDir := filepath.Join(root, "etc/systemd/system/multi-user.target.wants")
	assert.DirExists(t, wantsDir)
}

// ─── chrony.service symlink ───────────────────────────────────────────────────

func TestApplyEnvdPresetFiles_ChronySymlinkDefaultsToEtcSystemd(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)
	createFakeServiceFile(t, root, "etc/systemd/system/chrony.service")

	require.NoError(t, applyEnvdPresetFiles(root))

	symlink := filepath.Join(root, "etc/systemd/system/multi-user.target.wants/chrony.service")
	target := readSymlink(t, symlink)
	assert.Equal(t, "/etc/systemd/system/chrony.service", target)
}

func TestApplyEnvdPresetFiles_ChronySymlinkFallsBackToChronyd(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)
	// RHEL/CentOS uses chronyd.service, not chrony.service.
	createFakeServiceFile(t, root, "usr/lib/systemd/system/chronyd.service")

	require.NoError(t, applyEnvdPresetFiles(root))

	symlink := filepath.Join(root, "etc/systemd/system/multi-user.target.wants/chrony.service")
	target := readSymlink(t, symlink)
	assert.Equal(t, "/usr/lib/systemd/system/chronyd.service", target)
}

func TestApplyEnvdPresetFiles_ChronySymlinkFallsBackToUsrLibChrony(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)
	// Some distros ship chrony.service under /usr/lib/systemd/system/.
	createFakeServiceFile(t, root, "usr/lib/systemd/system/chrony.service")

	require.NoError(t, applyEnvdPresetFiles(root))

	symlink := filepath.Join(root, "etc/systemd/system/multi-user.target.wants/chrony.service")
	target := readSymlink(t, symlink)
	assert.Equal(t, "/usr/lib/systemd/system/chrony.service", target)
}

func TestApplyEnvdPresetFiles_ChronySymlinkChronydBeforeUsrLibChrony(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)
	// Both chronyd.service and chrony.service exist; chronyd should win.
	createFakeServiceFile(t, root, "usr/lib/systemd/system/chronyd.service")
	createFakeServiceFile(t, root, "usr/lib/systemd/system/chrony.service")

	require.NoError(t, applyEnvdPresetFiles(root))

	symlink := filepath.Join(root, "etc/systemd/system/multi-user.target.wants/chrony.service")
	target := readSymlink(t, symlink)
	assert.Equal(t, "/usr/lib/systemd/system/chronyd.service", target,
		"chronyd.service should take priority over chrony.service in usr/lib")
}

// ─── idempotency ─────────────────────────────────────────────────────────────

func TestApplyEnvdPresetFiles_Idempotent(t *testing.T) {
	t.Parallel()
	root := setupFakeRootfs(t)
	createFakeServiceFile(t, root, "etc/systemd/system/envd.service")
	createFakeServiceFile(t, root, "etc/systemd/system/chrony.service")

	// Run twice; second call should succeed without error.
	require.NoError(t, applyEnvdPresetFiles(root))
	require.NoError(t, applyEnvdPresetFiles(root))

	// Files should still have the correct content.
	content, err := os.ReadFile(filepath.Join(root, "etc/systemd/system-preset/80-envd.preset"))
	require.NoError(t, err)
	assert.Equal(t, "enable envd.service\n", string(content))
}
