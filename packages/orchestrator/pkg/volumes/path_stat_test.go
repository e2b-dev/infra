package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestStat(t *testing.T) {
	t.Parallel()

	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("stat file", func(t *testing.T) {
		t.Parallel()

		filename := "test.txt"
		err := os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644)
		require.NoError(t, err)

		resp, err := s.StatPath(t.Context(), &orchestrator.StatPathRequest{
			Volume: volumeInfo,
			Path:   filename,
		})
		require.NoError(t, err)
		require.Equal(t, orchestrator.FileType_FILE_TYPE_FILE, resp.GetEntry().GetType())
		require.Equal(t, "/"+filename, resp.GetEntry().GetPath())
	})

	t.Run("stat dir", func(t *testing.T) {
		t.Parallel()

		dirname := "test-dir"
		err := os.Mkdir(filepath.Join(tmpdir, dirname), 0o755)
		require.NoError(t, err)

		resp, err := s.StatPath(t.Context(), &orchestrator.StatPathRequest{
			Volume: volumeInfo,
			Path:   dirname,
		})
		require.NoError(t, err)
		require.Equal(t, orchestrator.FileType_FILE_TYPE_DIRECTORY, resp.GetEntry().GetType())
	})

	t.Run("stat non-existent", func(t *testing.T) {
		t.Parallel()

		_, err := s.StatPath(t.Context(), &orchestrator.StatPathRequest{
			Volume: volumeInfo,
			Path:   "non-existent",
		})
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})

	t.Run("stat symlink", func(t *testing.T) {
		t.Parallel()

		target := "target.txt"
		link := "link.txt"
		err := os.WriteFile(filepath.Join(tmpdir, target), []byte("test"), 0o644)
		require.NoError(t, err)
		err = os.Symlink(target, filepath.Join(tmpdir, link))
		require.NoError(t, err)

		resp, err := s.StatPath(t.Context(), &orchestrator.StatPathRequest{
			Volume: volumeInfo,
			Path:   link,
		})
		require.NoError(t, err)
		require.Equal(t, orchestrator.FileType_FILE_TYPE_FILE, resp.GetEntry().GetType())
	})
}
