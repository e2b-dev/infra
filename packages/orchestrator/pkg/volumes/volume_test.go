package volumes

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestVolume(t *testing.T) {
	t.Parallel()

	s, rootPath, volumeInfo := setupTestService(t)

	// create volume
	_, err := s.CreateVolume(t.Context(), &orchestrator.CreateVolumeRequest{
		Volume: volumeInfo,
	})
	require.NoError(t, err)

	_, err = os.Stat(rootPath)
	require.NoError(t, err)

	// delete volume
	_, err = s.DeleteVolume(t.Context(), &orchestrator.DeleteVolumeRequest{
		Volume: volumeInfo,
	})
	require.NoError(t, err)

	_, err = os.Stat(rootPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}
