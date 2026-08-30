//go:build linux

package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalBackend_Type(t *testing.T) {
	t.Parallel()
	b := NewLocalBackend(t.TempDir())
	assert.Equal(t, "local", b.Type())
}

func TestLocalBackend_RootPath_Format(t *testing.T) {
	t.Parallel()

	root := "/data/volumes"
	teamID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	volumeID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")

	b := NewLocalBackend(root)
	got := b.RootPath(teamID, volumeID)

	want := fmt.Sprintf("%s/team-%s/vol-%s", root, teamID, volumeID)
	assert.Equal(t, want, got)
}

func TestLocalBackend_CreateVolume_CreatesDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewLocalBackend(root)

	err := b.CreateVolume(context.Background(), teamID, volumeID)
	require.NoError(t, err)

	info, err := os.Stat(b.RootPath(teamID, volumeID))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestLocalBackend_CreateVolume_Idempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewLocalBackend(root)

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	// Second call must not error.
	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
}

func TestLocalBackend_CreateVolume_NestedDirs(t *testing.T) {
	t.Parallel()

	// The team/vol subdirectory structure must be created even when neither
	// the team nor the vol subdirectory exists yet.
	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewLocalBackend(root)

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	assert.DirExists(t, filepath.Join(root, fmt.Sprintf("team-%s", teamID)))
	assert.DirExists(t, b.RootPath(teamID, volumeID))
}

func TestLocalBackend_DeleteVolume_RemovesDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewLocalBackend(root)

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	require.DirExists(t, b.RootPath(teamID, volumeID))

	require.NoError(t, b.DeleteVolume(context.Background(), teamID, volumeID))
	assert.NoDirExists(t, b.RootPath(teamID, volumeID))
}

func TestLocalBackend_DeleteVolume_NonExistentDir_NoError(t *testing.T) {
	t.Parallel()

	b := NewLocalBackend(t.TempDir())
	// RemoveAll on a non-existent path must not error.
	err := b.DeleteVolume(context.Background(), uuid.New(), uuid.New())
	require.NoError(t, err)
}

func TestLocalBackend_Healthy_RootExists(t *testing.T) {
	t.Parallel()

	b := NewLocalBackend(t.TempDir())
	require.NoError(t, b.Healthy(context.Background()))
}

func TestLocalBackend_Healthy_RootMissing(t *testing.T) {
	t.Parallel()

	b := NewLocalBackend("/nonexistent/path/that/does/not/exist")
	err := b.Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accessible")
}

func TestLocalBackend_DeleteVolume_RemovesFilesInsideVolume(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewLocalBackend(root)

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))

	// Write a file inside the volume.
	volPath := b.RootPath(teamID, volumeID)
	err := os.WriteFile(filepath.Join(volPath, "data.txt"), []byte("hello"), 0o600)
	require.NoError(t, err)

	require.NoError(t, b.DeleteVolume(context.Background(), teamID, volumeID))
	assert.NoDirExists(t, volPath)
}
