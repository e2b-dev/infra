package volumes

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVolumeContent(t *testing.T) {
	client := setup.GetAPIClient()

	volumeName := uuid.NewString()

	createVolumeResponse, err := client.PostVolumesWithResponse(t.Context(), api.PostVolumesJSONRequestBody{
		Name: volumeName,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createVolumeResponse.StatusCode(), string(createVolumeResponse.Body))
	volume := createVolumeResponse.JSON201
	require.NotNil(t, volume, string(createVolumeResponse.Body))

	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		_, err := client.DeleteVolumesVolumeID(ctx, volume.VolumeID, setup.WithAPIKey())
		assert.NoError(t, err)
	})

	createFile := func(t *testing.T, path, content string) {
		t.Helper()

		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: path,
			},
			"application/octet-stream",
			bytes.NewBuffer([]byte(content)),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
	}

	retrieveFile := func(t *testing.T, path string) string {
		t.Helper()

		response, err := client.GetVolumesVolumeIDFileWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.GetVolumesVolumeIDFileParams{Path: path},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode(), string(response.Body))
		return string(response.Body)
	}

	t.Run("get volume content", func(t *testing.T) {
		filename := "test.txt"
		expected := "test content"

		createFile(t, filename, expected)

		actual := retrieveFile(t, filename)
		assert.Equal(t, expected, actual)
	})
}
