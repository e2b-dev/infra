package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestDirCreate(t *testing.T) {
	t.Parallel()

	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("create dir", func(t *testing.T) {
		t.Parallel()

		dirname := "test-dir"
		_, err := s.CreateDir(t.Context(), &orchestrator.CreateDirRequest{
			Volume: volumeInfo,
			Path:   dirname,
		})
		require.NoError(t, err)

		fi, err := os.Stat(filepath.Join(tmpdir, dirname))
		require.NoError(t, err)
		require.True(t, fi.IsDir())
	})

	t.Run("create nested dir with CreateParents=true", func(t *testing.T) {
		t.Parallel()

		dirname := "parent/child"
		_, err := s.CreateDir(t.Context(), &orchestrator.CreateDirRequest{
			Volume:        volumeInfo,
			Path:          dirname,
			CreateParents: true,
		})
		require.NoError(t, err)

		fi, err := os.Stat(filepath.Join(tmpdir, dirname))
		require.NoError(t, err)
		require.True(t, fi.IsDir())
	})

	t.Run("create nested dir without CreateParents (should fail)", func(t *testing.T) {
		t.Parallel()

		dirname := "another-parent/child"
		_, err := s.CreateDir(t.Context(), &orchestrator.CreateDirRequest{
			Volume:        volumeInfo,
			Path:          dirname,
			CreateParents: false,
		})
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})

	t.Run("create dir with custom mode and ownership", func(t *testing.T) {
		t.Parallel()

		dirname := "custom-dir"
		mode := uint32(0o700)
		uid := uint32(1000)
		gid := uint32(1000)

		_, err := s.CreateDir(t.Context(), &orchestrator.CreateDirRequest{
			Volume: volumeInfo,
			Path:   dirname,
			Mode:   utils.ToPtr(mode),
			Uid:    utils.ToPtr(uid),
			Gid:    utils.ToPtr(gid),
		})
		require.NoError(t, err)

		fi, err := os.Stat(filepath.Join(tmpdir, dirname))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(mode), fi.Mode().Perm())
	})

	t.Run("create dir that already exists", func(t *testing.T) {
		t.Parallel()

		dirname := "existing-dir"
		err := os.Mkdir(filepath.Join(tmpdir, dirname), 0o755)
		require.NoError(t, err)

		_, err = s.CreateDir(t.Context(), &orchestrator.CreateDirRequest{
			Volume: volumeInfo,
			Path:   dirname,
		})
		requireGRPCError(t, err, codes.AlreadyExists, orchestrator.UserErrorCode_PATH_ALREADY_EXISTS)
	})

	t.Run("CreateDir with CreateParents=true should fail when path is a file", func(t *testing.T) {
		t.Parallel()

		filename := "existing-file"
		fullPath := filepath.Join(tmpdir, filename)
		err := os.WriteFile(fullPath, []byte("test"), 0o644)
		require.NoError(t, err)

		_, err = s.CreateDir(t.Context(), &orchestrator.CreateDirRequest{
			Volume:        volumeInfo,
			Path:          filename,
			CreateParents: true,
		})
		requireGRPCError(t, err, codes.AlreadyExists, orchestrator.UserErrorCode_PATH_ALREADY_EXISTS)
	})

	t.Run("CreateDir with CreateParents=true should not change existing directory", func(t *testing.T) {
		t.Parallel()

		dirname := "existing-dir-to-preserve"
		fullPath := filepath.Join(tmpdir, dirname)

		originalMode := os.FileMode(0o700)
		err := os.MkdirAll(fullPath, originalMode)
		require.NoError(t, err)

		// Ensure it's 0700 (MkdirAll might be affected by umask, so Chmod it)
		err = os.Chmod(fullPath, originalMode)
		require.NoError(t, err)

		// Ensure the user doesn't change either
		err = os.Chown(fullPath, 1500, 1600)
		require.NoError(t, err)

		// Now call CreateDir with CreateParents=true and a different mode
		newMode := uint32(0o777)
		request := &orchestrator.CreateDirRequest{
			Volume:        volumeInfo,
			Path:          dirname,
			CreateParents: true,
			Mode:          utils.ToPtr(newMode),
			Uid:           utils.ToPtr(uint32(1100)),
			Gid:           utils.ToPtr(uint32(1200)),
		}
		_, err = s.CreateDir(t.Context(), request)
		require.NoError(t, err)

		fs, path, errResponse := s.getFilesystemAndPath(t.Context(), request)
		require.Nil(t, errResponse)
		assert.Equal(t, "/existing-dir-to-preserve", path)

		assertDir(t, fs, dirname, 1500, 1600, originalMode)
		// Check if the mode was changed
		fi, err := os.Stat(fullPath)
		require.NoError(t, err)

		require.Equal(t, originalMode, fi.Mode().Perm(), "Mode should not have been changed for an existing directory when CreateParents=true")
	})
}
