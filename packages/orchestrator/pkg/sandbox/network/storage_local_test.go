//go:build linux

package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSlotIndexFromNamespace(t *testing.T) {
	t.Parallel()

	idx, ok := SlotIndexFromNamespace("ns-2")
	require.True(t, ok)
	require.Equal(t, 2, idx)

	for _, name := range []string{"host", "ns-0", "ns-nope", "other-2"} {
		_, ok := SlotIndexFromNamespace(name)
		require.False(t, ok)
	}
}

func TestListSlotNamespaces(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"ns-10", "ns-2", "host", "ns-bad"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "ns-3"), 0o700))

	indices, err := ListSlotNamespaces(dir)
	require.NoError(t, err)
	require.Equal(t, []int{2, 10}, indices)
}
