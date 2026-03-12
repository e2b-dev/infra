package volumes

import (
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestVolume(t *testing.T) {
	s, _, _ := setupTestService(t)

	teamID := uuid.New().String()
	volumeID := uuid.New().String()
	volumeInfo := &orchestrator.VolumeInfo{
		VolumeType: volumeType,
		TeamId:     teamID,
		VolumeId:   volumeID,
	}

	t.Run("create volume", func(t *testing.T) {
		_, err := s.Create(t.Context(), &orchestrator.VolumeCreateRequest{
			Volume: volumeInfo,
		})
		require.NoError(t, err)

		rootPath, err := s.getVolumeRootPath(volumeInfo)
		require.NoError(t, err)

		_, err = os.Stat(rootPath)
		require.NoError(t, err)
	})

	t.Run("delete volume", func(t *testing.T) {
		_, err := s.Delete(t.Context(), &orchestrator.VolumeDeleteRequest{
			Volume: volumeInfo,
		})
		require.NoError(t, err)

		rootPath, err := s.getVolumeRootPath(volumeInfo)
		require.NoError(t, err)

		_, err = os.Stat(rootPath)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}
