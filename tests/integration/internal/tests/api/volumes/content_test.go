package volumes

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
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

	createVolume := func() *api.Volume {
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

		return volume
	}

	volume := createVolume()

	createFileInVolume := func(t *testing.T, vol *api.Volume, path, content string) *api.VolumeEntryStat {
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
		return response.JSON201
	}

	createFile := func(t *testing.T, path, content string) *api.VolumeEntryStat {
		return createFileInVolume(t, volume, path, content)
	}

	readFileInVolume := func(t *testing.T, volume *api.Volume, path string) string {
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

	createDirInVolume := func(t *testing.T, volume *api.Volume, path string) {
		response, err := client.PostVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.PostVolumesVolumeIDDirParams{Path: path},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
	}

	createDir := func(t *testing.T, path string) {
		createDirInVolume(t, volume, path)
	}

	readFile := func(t *testing.T, path string) string {
		return readFileInVolume(t, volume, path)
	}

	t.Run("get volume content", func(t *testing.T) {
		t.Parallel()

		filename := "test.txt"
		expected := "test content"

		response := createFile(t, filename, expected)
		assert.Equal(t, uint32(0o666), response.Mode)
		assert.Equal(t, uint32(1000), response.Uid)
		assert.Equal(t, uint32(1000), response.Gid)
		assert.Equal(t, api.File, response.Type)
		assert.Equal(t, response.Size, int64(len(expected)))
		assert.Equal(t, filename, response.Name)
		assert.False(t, response.Ctime.IsZero())
		assert.False(t, response.Mtime.IsZero())

		actual := readFile(t, filename)
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
		actual := readFile(t, filename)
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
		actual = readFile(t, filename)
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

	t.Run("can set permissions while creating file", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		content := uuid.NewString()

		// create the file
		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: filename,
				Mode: utils.ToPtr(uint32(0o642)),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
		entry := response.JSON201
		assert.Equal(t, uint32(0o642), entry.Mode)
	})

	t.Run("cannot read file across volumes", func(t *testing.T) {
		t.Parallel()

		filename := uuid.NewString()

		vol1 := createVolume()
		vol2 := createVolume()

		createFileInVolume(t, vol1, filename, uuid.NewString())

		response, err := client.GetVolumesVolumeIDFileWithResponse(
			t.Context(),
			vol2.VolumeID,
			&api.GetVolumesVolumeIDFileParams{Path: filepath.Join("..", vol1.VolumeID, "test.txt")},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, response.StatusCode(), string(response.Body))
	})

	t.Run("can delete file", func(t *testing.T) {
		filename := uuid.NewString()

		createFile(t, filename, uuid.NewString())

		response, err := client.DeleteVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.DeleteVolumesVolumeIDFileParams{Path: filename},
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, response.StatusCode())
	})

	t.Run("cannot delete file that does not exist", func(t *testing.T) {
		filename := uuid.NewString()

		response, err := client.DeleteVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.DeleteVolumesVolumeIDFileParams{Path: filename},
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, response.StatusCode(), string(response.Body))
	})

	t.Run("cannot create file in non existent subdirectory", func(t *testing.T) {
		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(), volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: filepath.Join(dirName, fileName),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, response.StatusCode(), string(response.Body))
	})

	t.Run("can create file in non existent subdirectory", func(t *testing.T) {
		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(), volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path:  filepath.Join(dirName, fileName),
				Force: utils.ToPtr(true),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
	})

	t.Run("can create file in created subdirectory", func(t *testing.T) {
		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		createDir(t, dirName)

		response, err := client.PostVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(), volume.VolumeID,
			&api.PostVolumesVolumeIDFileParams{
				Path: filepath.Join(dirName, fileName),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
	})

	t.Run("cannot delete subdirectory with contents without force", func(t *testing.T) {
		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		createDir(t, dirName)

		createFile(t, filepath.Join(dirName, fileName), content)

		response, err := client.DeleteVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.DeleteVolumesVolumeIDDirParams{
				Path: filepath.Join(dirName, fileName),
			},
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, response.StatusCode(), string(response.Body))
	})

	t.Run("can delete subdirectory with contents and recursive", func(t *testing.T) {
		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		createDir(t, dirName)

		createFile(t, filepath.Join(dirName, fileName), content)

		response, err := client.DeleteVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.DeleteVolumesVolumeIDDirParams{
				Path:      filepath.Join(dirName, fileName),
				Recursive: utils.ToPtr(true),
			},
			setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, response.StatusCode(), string(response.Body))

		// cannot retrieve file, b/c it's gone
		getResponse, err := client.GetVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.GetVolumesVolumeIDFileParams{Path: filepath.Join(dirName, fileName)},
			setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, getResponse.StatusCode(), string(getResponse.Body))

		// cannot retrieve directory, b/c it's gone
		getDirResponse, err := client.GetVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.GetVolumesVolumeIDDirParams{Path: dirName},
			setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, getDirResponse.StatusCode(), string(getDirResponse.Body))
	})
}
