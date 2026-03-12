package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestFileDelete(t *testing.T) {
	t.Parallel()

	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("delete file", func(t *testing.T) {
		t.Parallel()

		filename := "test-delete.txt"
		err := os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644)
		require.NoError(t, err)

		_, err = s.DeleteFile(t.Context(), &orchestrator.VolumeFileDeleteRequest{
			Volume: volumeInfo,
			Path:   filename,
		})
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(tmpdir, filename))
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("delete non-existent file", func(t *testing.T) {
		t.Parallel()

		_, err := s.DeleteFile(t.Context(), &orchestrator.VolumeFileDeleteRequest{
			Volume: volumeInfo,
			Path:   "non-existent-file",
		})
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})

	t.Run("delete root (should fail)", func(t *testing.T) {
		t.Parallel()

		_, err := s.DeleteFile(t.Context(), &orchestrator.VolumeFileDeleteRequest{
			Volume: volumeInfo,
			Path:   "/",
		})
		requireGRPCError(t, err, codes.InvalidArgument, orchestrator.UserErrorCode_CANNOT_DELETE_ROOT)
	})
}
