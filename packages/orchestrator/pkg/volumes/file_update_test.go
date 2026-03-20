package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestFileUpdateMetadata(t *testing.T) {
	t.Parallel()

	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("update mode", func(t *testing.T) {
		t.Parallel()

		filename := "test-update-mode.txt"
		err := os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644)
		require.NoError(t, err)

		newMode := uint32(0o755)
		_, err = s.UpdateFileMetadata(t.Context(), &orchestrator.VolumeFileUpdateRequest{
			Volume: volumeInfo,
			Path:   filename,
			Mode:   &newMode,
		})
		require.NoError(t, err)

		fi, err := os.Stat(filepath.Join(tmpdir, filename))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(newMode), fi.Mode().Perm())
	})

	t.Run("update uid/gid", func(t *testing.T) {
		t.Parallel()

		filename := "test-update-owner.txt"
		err := os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644)
		require.NoError(t, err)

		newUid := uint32(1001)
		newGid := uint32(1001)
		_, err = s.UpdateFileMetadata(t.Context(), &orchestrator.VolumeFileUpdateRequest{
			Volume: volumeInfo,
			Path:   filename,
			Uid:    &newUid,
			Gid:    &newGid,
		})
		require.NoError(t, err)
		// Ownership check would ideally be here if we were running as root.
	})

	t.Run("update non-existent file", func(t *testing.T) {
		t.Parallel()

		newMode := uint32(0o755)
		_, err := s.UpdateFileMetadata(t.Context(), &orchestrator.VolumeFileUpdateRequest{
			Volume: volumeInfo,
			Path:   "non-existent",
			Mode:   &newMode,
		})
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})
}
