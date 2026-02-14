package volumes

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestVolumeContent(t *testing.T) {
	t.Parallel()

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
		_, err := client.DeleteVolumesVolumeIDWithResponse(ctx, volume.VolumeID, setup.WithAPIKey())
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
			bytes.NewBufferString(content),
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
		t.Parallel()

		filename := "test.txt"
		expected := "test content"

		createFile(t, filename, expected)

		actual := retrieveFile(t, filename)
		assert.Equal(t, expected, actual)
	})

	t.Run("cannot overwrite file without force", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		originalContent := uuid.NewString()
		newContent := uuid.NewString()

		// create the file
		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: filename,
			},
			"application/octet-stream",
			bytes.NewBufferString(originalContent),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))

		// attempt to overwrite the file, fail
		response, err = client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: filename,
			},
			"application/octet-stream",
			bytes.NewBufferString(newContent),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusConflict, response.StatusCode(), string(response.Body))

		// check that the file content hasn't changed
		actual := retrieveFile(t, filename)
		assert.Equal(t, originalContent, actual)

		// use force flag
		response, err = client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path:  filename,
				Force: utils.ToPtr(true),
			},
			"application/octet-stream",
			bytes.NewBufferString(newContent),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))

		// check that the file content has been updated
		actual = retrieveFile(t, filename)
		assert.Equal(t, newContent, actual)
	})

	t.Run("can set user and group while creating file", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		content := uuid.NewString()

		// create the file
		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: filename,
				Uid:  utils.ToPtr(uint32(12345)),
				Gid:  utils.ToPtr(uint32(54321)),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
		entry := response.JSON201
		assert.Equal(t, uint32(12345), entry.Uid)
		assert.Equal(t, uint32(54321), entry.Gid)
	})

	t.Run("can set only user while creating file", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		content := uuid.NewString()

		// create the file
		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: filename,
				Uid:  utils.ToPtr(uint32(12345)),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
		entry := response.JSON201
		assert.Equal(t, uint32(12345), entry.Uid)
		assert.Equal(t, uint32(1000), entry.Gid)
	})

	t.Run("can set only group while creating file", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		content := uuid.NewString()

		// create the file
		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: filename,
				Gid:  utils.ToPtr(uint32(12345)),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
		entry := response.JSON201
		assert.Equal(t, uint32(1000), entry.Uid)
		assert.Equal(t, uint32(12345), entry.Gid)
	})
}
