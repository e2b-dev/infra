package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDirDelete(t *testing.T) {
	s, tmpdir, volumeInfo := setupTestService(t)

	dirname := "test-dir"

	t.Run("delete dir", func(t *testing.T) {
		// create directory
		err := os.Mkdir(filepath.Join(tmpdir, dirname), 0755)
		require.NoError(t, err)

		// delete directory
		_, err = s.DeleteDir(t.Context(), &orchestrator.VolumeDirDeleteRequest{
			Volume: volumeInfo,
			Path:   dirname,
		})
		require.NoError(t, err)

		// verify the directory is gone
		_, err = os.Stat(filepath.Join(tmpdir, dirname))
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("delete non-existent dir", func(t *testing.T) {
		_, err := s.DeleteDir(t.Context(), &orchestrator.VolumeDirDeleteRequest{
			Volume: volumeInfo,
			Path:   "non-existent-dir",
		})

		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})
}

func requireGRPCError(t *testing.T, err error, expectedGRPCCode codes.Code, expectedUserErrorCode orchestrator.UserErrorCode) {
	t.Helper()

	require.Error(t, err)

	status, ok := status.FromError(err)
	require.True(t, ok)

	require.Equal(t, status.Code(), expectedGRPCCode)

	for _, detail := range status.Details() {
		if userError, ok := detail.(*orchestrator.UserError); ok {
			require.Equal(t, userError.Code, expectedUserErrorCode)
			return
		}
	}

	require.Fail(t, "expected UserError detail not found")
}
