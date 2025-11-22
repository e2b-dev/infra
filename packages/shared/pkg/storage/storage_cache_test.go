package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoveWithoutReplace_SuccessWhenDestMissing(t *testing.T) {
	td := t.TempDir()
	content := []byte("alpha")
	src := filepath.Join(td, "src")
	dst := filepath.Join(td, "dst")

	require.NoError(t, os.WriteFile(src, content, 0o644))
	err := moveWithoutReplace(src, dst)
	require.NoError(t, err)

	// Dest has original content.
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	_, err = os.Stat(src)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestMoveWithoutReplace_FailWhenExists(t *testing.T) {
	td := t.TempDir()
	content := []byte("alpha")
	secondContent := []byte("beta")
	src := filepath.Join(td, "src")
	dst := filepath.Join(td, "dst")

	require.NoError(t, os.WriteFile(src, content, 0o644))
	require.NoError(t, os.WriteFile(dst, secondContent, 0o644))
	err := moveWithoutReplace(src, dst)
	require.NoError(t, err)

	// Dest has original content.
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, secondContent, got)

	_, err = os.Stat(src)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
