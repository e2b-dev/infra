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

	auth := []api.RequestEditorFn{
		setup.WithAccessToken(),
	}

	// create volume
	createVolume, err := client.PostVolumesWithResponse(
		t.Context(),
		api.PostVolumesJSONRequestBody{Name: volumeName},
		auth...,
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createVolume.StatusCode(), string(createVolume.Body))
	assert.Equal(t, volumeName, createVolume.JSON201.Name)
	assert.NotEmpty(t, createVolume.JSON201.Id)
	volumeID := createVolume.JSON201.Id

	// fail to create volume again
	createVolumeFailure, err := client.PostVolumesWithResponse(
		t.Context(),
		api.PostVolumesJSONRequestBody{Name: volumeName},
		auth...,
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, createVolumeFailure.StatusCode(), string(createVolumeFailure.Body))

	// retrieve volume
	getVolume, err := client.GetVolumesVolumeIDWithResponse(t.Context(), volumeID, auth...)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getVolume.StatusCode(), string(getVolume.Body))
	assert.Equal(t, volumeName, getVolume.JSON200.Name)
	assert.Equal(t, volumeID, getVolume.JSON200.Id)

	// list volumes
	listVolumes, err := client.GetVolumesWithResponse(t.Context(), auth...)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listVolumes.StatusCode(), string(listVolumes.Body))
	assert.Contains(t, *listVolumes.JSON200, *getVolume.JSON200)

	// delete volume
	deleteVolume, err := client.DeleteVolumesVolumeIDWithResponse(t.Context(), volumeID, auth...)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, deleteVolume.StatusCode(), string(deleteVolume.Body))

	// verify volume is deleted
	getVolumeFailed, err := client.GetVolumesVolumeIDWithResponse(t.Context(), volumeID, auth...)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, getVolumeFailed.StatusCode(), string(getVolumeFailed.Body))

	// verify volume is not in list
	listVolumes, err = client.GetVolumesWithResponse(t.Context(), auth...)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listVolumes.StatusCode(), string(listVolumes.Body))
	assert.NotContains(t, *listVolumes.JSON200, *getVolume.JSON200)

	// verify volume cannot be deleted again
	deleteVolume, err = client.DeleteVolumesVolumeIDWithResponse(t.Context(), volumeID, auth...)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, deleteVolume.StatusCode(), string(deleteVolume.Body))
}
