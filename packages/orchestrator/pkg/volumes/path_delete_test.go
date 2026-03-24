package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestDeletePath(t *testing.T) {
	t.Parallel()

	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("delete file", func(t *testing.T) {
		t.Parallel()

		filename := "file-to-delete.txt"
		fullPath := filepath.Join(tmpdir, filename)

		err := os.WriteFile(fullPath, []byte("test content"), 0o644)
		require.NoError(t, err)

		_, err = s.DeletePath(t.Context(), &orchestrator.DeletePathRequest{
			Volume: volumeInfo,
			Path:   filename,
		})
		require.NoError(t, err)

		_, err = os.Stat(fullPath)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("delete empty directory", func(t *testing.T) {
		t.Parallel()

		dirname := "empty-dir-to-delete"
		fullPath := filepath.Join(tmpdir, dirname)

		err := os.Mkdir(fullPath, 0o755)
		require.NoError(t, err)

		_, err = s.DeletePath(t.Context(), &orchestrator.DeletePathRequest{
			Volume: volumeInfo,
			Path:   dirname,
		})
		require.NoError(t, err)

		_, err = os.Stat(fullPath)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("delete non-empty directory recursively", func(t *testing.T) {
		t.Parallel()

		dirname := "non-empty-dir"
		fullPath := filepath.Join(tmpdir, dirname)

		err := os.Mkdir(fullPath, 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(fullPath, "child.txt"), []byte("child content"), 0o644)
		require.NoError(t, err)

		// Create nested subdirectory with file
		nestedDir := filepath.Join(fullPath, "nested")
		err = os.Mkdir(nestedDir, 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(nestedDir, "nested-child.txt"), []byte("nested content"), 0o644)
		require.NoError(t, err)

		_, err = s.DeletePath(t.Context(), &orchestrator.DeletePathRequest{
			Volume: volumeInfo,
			Path:   dirname,
		})
		require.NoError(t, err)

		// Directory and all contents should be deleted
		_, err = os.Stat(fullPath)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("delete non-existent path", func(t *testing.T) {
		t.Parallel()

		_, err := s.DeletePath(t.Context(), &orchestrator.DeletePathRequest{
			Volume: volumeInfo,
			Path:   "non-existent-path",
		})
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})

	t.Run("delete root fails", func(t *testing.T) {
		t.Parallel()

		_, err := s.DeletePath(t.Context(), &orchestrator.DeletePathRequest{
			Volume: volumeInfo,
			Path:   "/",
		})
		requireGRPCError(t, err, codes.InvalidArgument, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT)
	})

	t.Run("delete symlink", func(t *testing.T) {
		t.Parallel()

		target := "symlink-target.txt"
		link := "symlink-to-delete"
		targetPath := filepath.Join(tmpdir, target)
		linkPath := filepath.Join(tmpdir, link)

		err := os.WriteFile(targetPath, []byte("target content"), 0o644)
		require.NoError(t, err)

		err = os.Symlink(target, linkPath)
		require.NoError(t, err)

		_, err = s.DeletePath(t.Context(), &orchestrator.DeletePathRequest{
			Volume: volumeInfo,
			Path:   link,
		})
		require.NoError(t, err)

		// Symlink should be deleted
		_, err = os.Lstat(linkPath)
		require.ErrorIs(t, err, os.ErrNotExist)

		// Target should still exist
		_, err = os.Stat(targetPath)
		require.NoError(t, err)
	})

	t.Run("delete broken symlink", func(t *testing.T) {
		t.Parallel()

		target := "broken-symlink-target.txt"
		link := "broken-symlink-to-delete"
		linkPath := filepath.Join(tmpdir, link)

		err := os.Symlink(target, linkPath)
		require.NoError(t, err)

		_, err = s.DeletePath(t.Context(), &orchestrator.DeletePathRequest{
			Volume: volumeInfo,
			Path:   link,
		})
		require.NoError(t, err)

		// Symlink should be deleted
		_, err = os.Lstat(linkPath)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}
