package volumes

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestVolumeRoundTrip(t *testing.T) {
	t.Parallel()

	volumeName := uuid.NewString()

	client := setup.GetAPIClient()

	// create volume
	createVolume, err := client.PostVolumesWithResponse(
		t.Context(),
		api.PostVolumesJSONRequestBody{Name: volumeName},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createVolume.StatusCode())
	assert.Equal(t, volumeName, createVolume.JSON201.Name)
	assert.NotEmpty(t, createVolume.JSON201.Id)
	volumeID := createVolume.JSON201.Id

	// fail to create volume again
	createVolumeFailure, err := client.PostVolumesWithResponse(
		t.Context(),
		api.PostVolumesJSONRequestBody{Name: volumeName},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, createVolumeFailure.StatusCode())

	// retrieve volume
	getVolume, err := client.GetVolumesVolumeIDWithResponse(t.Context(), volumeID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getVolume.StatusCode())
	assert.Equal(t, volumeName, getVolume.JSON200.Name)
	assert.Equal(t, volumeID, getVolume.JSON200.Id)

	// list volumes
	listVolumes, err := client.GetVolumesWithResponse(t.Context(), setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listVolumes.StatusCode())
	assert.Contains(t, *listVolumes.JSON200, *getVolume.JSON200)

	// delete volume
	deleteVolume, err := client.DeleteVolumesVolumeIDWithResponse(t.Context(), volumeID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, deleteVolume.StatusCode())

	// verify volume is deleted
	getVolume, err = client.GetVolumesVolumeIDWithResponse(t.Context(), volumeID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, getVolume.StatusCode())

	// verify volume is not in list
	listVolumes, err = client.GetVolumesWithResponse(t.Context(), setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listVolumes.StatusCode())
	assert.NotContains(t, *listVolumes.JSON200, *getVolume.JSON200)

	// verify volume cannot be deleted again
	deleteVolume, err = client.DeleteVolumesVolumeIDWithResponse(t.Context(), volumeID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, deleteVolume.StatusCode())
}
