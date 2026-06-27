//go:build linux

package cgroup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSandboxCgroupName(t *testing.T) {
	t.Parallel()

	require.True(t, IsSandboxCgroupName("sbx-sandbox-rand"))
	require.False(t, IsSandboxCgroupName("sbx-"))
	require.False(t, IsSandboxCgroupName("other-sbx-sandbox"))
}

func TestListSandboxCgroups(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"sbx-b", "other", "sbx-a"} {
		require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0o700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sbx-file"), []byte("x"), 0o600))

	names, err := ListSandboxCgroups(dir)
	require.NoError(t, err)
	require.Equal(t, []string{"sbx-a", "sbx-b"}, names)
}
