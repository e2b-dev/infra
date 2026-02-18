package volumes

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			vol.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
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
		t.Helper()

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
		t.Helper()

		response, err := client.PostVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.PostVolumesVolumeIDDirParams{Path: path},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
	}

	createDir := func(t *testing.T, path string) {
		t.Helper()

		createDirInVolume(t, volume, path)
	}

	readFile := func(t *testing.T, path string) string {
		t.Helper()

		return readFileInVolume(t, volume, path)
	}

	getStatInVolume := func(t *testing.T, vol *api.Volume, path string) *api.VolumeEntryStat {
		t.Helper()

		resp, err := client.GetVolumesVolumeIDStatWithResponse(
			t.Context(), vol.VolumeID,
			&api.GetVolumesVolumeIDStatParams{Path: path},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode(), string(resp.Body))

		return resp.JSON200
	}

	getStat := func(t *testing.T, path string) *api.VolumeEntryStat {
		t.Helper()

		return getStatInVolume(t, volume, path)
	}

	t.Run("get volume content", func(t *testing.T) {
		t.Parallel()

		filename := "test.txt"
		expected := "test content"

		createdFile := createFile(t, filename, expected)
		assert.Equal(t, uint32(0o666), createdFile.Mode)
		assert.Equal(t, uint32(1000), createdFile.Uid)
		assert.Equal(t, uint32(1000), createdFile.Gid)
		assert.Equal(t, api.File, createdFile.Type)
		assert.Equal(t, int64(len(expected)), createdFile.Size)
		assert.Equal(t, filename, createdFile.Name)
		assert.Equal(t, "/test.txt", createdFile.Path)
		assert.False(t, createdFile.Ctime.IsZero())
		assert.False(t, createdFile.Mtime.IsZero())

		response, err := client.GetVolumesVolumeIDFileWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.GetVolumesVolumeIDFileParams{Path: filename},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode(), string(response.Body))
		assert.Equal(t, expected, string(response.Body))
		headers := response.HTTPResponse.Header
		assert.Equal(t, "application/octet-stream", headers.Get("Content-Type"))
		assert.Equal(t, strconv.Itoa(len(expected)), headers.Get("Content-Length"))
		assert.Equal(t, fmt.Sprintf("attachment; filename=%s", filename), headers.Get("Content-Disposition"))
	})

	t.Run("cannot overwrite file without force", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		originalContent := uuid.NewString()
		newContent := uuid.NewString()

		// create the file
		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
				Path: filename,
			},
			"application/octet-stream",
			bytes.NewBufferString(originalContent),
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))

		// attempt to overwrite the file, fail
		response, err = client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
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
		response, err = client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
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
		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
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
		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
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
		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
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
		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(),
			volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
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
		t.Parallel()

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
		t.Parallel()

		filename := uuid.NewString()

		response, err := client.DeleteVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.DeleteVolumesVolumeIDFileParams{Path: filename},
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, response.StatusCode(), string(response.Body))
	})

	t.Run("cannot create file in non existent subdirectory", func(t *testing.T) {
		t.Parallel()

		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(), volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
				Path: filepath.Join(dirName, fileName),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, response.StatusCode(), string(response.Body))
	})

	t.Run("can create file in non existent subdirectory with force=true", func(t *testing.T) {
		t.Parallel()

		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(), volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
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
		t.Parallel()

		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		createDir(t, dirName)

		response, err := client.PutVolumesVolumeIDFileWithBodyWithResponse(
			t.Context(), volume.VolumeID,
			&api.PutVolumesVolumeIDFileParams{
				Path: filepath.Join(dirName, fileName),
			},
			"application/octet-stream",
			bytes.NewBufferString(content),
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
		assert.Equal(t, fmt.Sprintf("/%s/%s", dirName, fileName), response.JSON201.Path)
		assert.Equal(t, fileName, response.JSON201.Name)
	})

	t.Run("cannot delete directory with contents without force", func(t *testing.T) {
		t.Parallel()

		dirName := uuid.NewString()
		fileName := uuid.NewString()
		content := uuid.NewString()

		createDir(t, dirName)

		createFile(t, filepath.Join(dirName, fileName), content)

		response, err := client.DeleteVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.DeleteVolumesVolumeIDDirParams{
				Path: dirName,
			},
			setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusPreconditionFailed, response.StatusCode(), string(response.Body))
	})

	t.Run("can delete subdirectory with contents and recursive", func(t *testing.T) {
		t.Parallel()

		dirName := uuid.NewString()
		files := []string{
			uuid.NewString(),
			uuid.NewString(),
			uuid.NewString(),
		}

		createDir(t, dirName)

		for _, fname := range files {
			createFile(t, filepath.Join(dirName, fname), fmt.Sprintf("%s content", dirName))
		}

		// verify that files are created
		listResponse, err := client.GetVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.GetVolumesVolumeIDDirParams{Path: dirName},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResponse.StatusCode(), string(listResponse.Body))
		require.NotNil(t, listResponse.JSON200)
		assert.Len(t, *listResponse.JSON200, len(files))

		// delete folder with contents
		response, err := client.DeleteVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.DeleteVolumesVolumeIDDirParams{
				Path:      dirName,
				Recursive: utils.ToPtr(true),
			},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, response.StatusCode(), string(response.Body))

		// cannot retrieve file, b/c it's gone
		for _, fileName := range files {
			getResponse, err := client.GetVolumesVolumeIDFileWithResponse(
				t.Context(), volume.VolumeID,
				&api.GetVolumesVolumeIDFileParams{Path: filepath.Join(dirName, fileName)},
				setup.WithAPIKey(),
			)
			require.NoError(t, err)
			assert.Equal(t, http.StatusNotFound, getResponse.StatusCode(), string(getResponse.Body))
		}

		// cannot list directory, b/c it's gone
		listDirResponse, err := client.GetVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.GetVolumesVolumeIDDirParams{Path: dirName},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, listDirResponse.StatusCode(), string(listDirResponse.Body))

		// cannot retrieve directory, b/c it's gone
		getDirResponse, err := client.GetVolumesVolumeIDDirWithResponse(
			t.Context(), volume.VolumeID,
			&api.GetVolumesVolumeIDDirParams{Path: dirName},
			setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, getDirResponse.StatusCode(), string(getDirResponse.Body))
	})

	// PATCH (chmod/chown) behavior
	t.Run("can chmod an existing file", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		content := uuid.NewString()

		created := createFile(t, filename, content)
		assert.Equal(t, uint32(0o666), created.Mode)
		assert.Equal(t, uint32(1000), created.Uid)
		assert.Equal(t, uint32(1000), created.Gid)

		// chmod to 0640
		patchResp, err := client.PatchVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.PatchVolumesVolumeIDFileParams{Path: filename},
			api.PatchVolumesVolumeIDFileJSONRequestBody{Mode: utils.ToPtr(uint32(0o640))},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, patchResp.StatusCode(), string(patchResp.Body))
		entry := patchResp.JSON200
		require.NotNil(t, entry)
		assert.Equal(t, uint32(0o640), entry.Mode)
		// ownership should remain unchanged
		assert.Equal(t, uint32(1000), entry.Uid)
		assert.Equal(t, uint32(1000), entry.Gid)

		// verify persisted via stat
		st := getStat(t, filename)
		assert.Equal(t, uint32(0o640), st.Mode)
		assert.Equal(t, uint32(1000), st.Uid)
		assert.Equal(t, uint32(1000), st.Gid)
	})

	t.Run("can chown an existing file (uid and gid)", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		content := uuid.NewString()

		created := createFile(t, filename, content)
		oldMode := created.Mode

		patchResp, err := client.PatchVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.PatchVolumesVolumeIDFileParams{Path: filename},
			api.PatchVolumesVolumeIDFileJSONRequestBody{Uid: utils.ToPtr(uint32(12345)), Gid: utils.ToPtr(uint32(54321))},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, patchResp.StatusCode(), string(patchResp.Body))
		entry := patchResp.JSON200
		require.NotNil(t, entry)
		assert.Equal(t, uint32(12345), entry.Uid)
		assert.Equal(t, uint32(54321), entry.Gid)
		// mode should remain unchanged
		assert.Equal(t, oldMode, entry.Mode)

		st := getStat(t, filename)
		assert.Equal(t, uint32(12345), st.Uid)
		assert.Equal(t, uint32(54321), st.Gid)
		assert.Equal(t, oldMode, st.Mode)
	})

	t.Run("can set only uid while patching file", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		content := uuid.NewString()

		created := createFile(t, filename, content)

		patchResp, err := client.PatchVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.PatchVolumesVolumeIDFileParams{Path: filename},
			api.PatchVolumesVolumeIDFileJSONRequestBody{Uid: utils.ToPtr(uint32(12345))},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, patchResp.StatusCode(), string(patchResp.Body))
		entry := patchResp.JSON200
		require.NotNil(t, entry)
		assert.Equal(t, uint32(12345), entry.Uid)
		assert.Equal(t, created.Gid, entry.Gid)
		assert.Equal(t, created.Mode, entry.Mode)

		st := getStat(t, filename)
		assert.Equal(t, uint32(12345), st.Uid)
		assert.Equal(t, created.Gid, st.Gid)
		assert.Equal(t, created.Mode, st.Mode)
	})

	t.Run("can set only gid while patching file", func(t *testing.T) {
		t.Parallel()

		filename := fmt.Sprintf("%s.txt", uuid.NewString())
		content := uuid.NewString()

		created := createFile(t, filename, content)

		patchResp, err := client.PatchVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.PatchVolumesVolumeIDFileParams{Path: filename},
			api.PatchVolumesVolumeIDFileJSONRequestBody{Gid: utils.ToPtr(uint32(23456))},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, patchResp.StatusCode(), string(patchResp.Body))
		entry := patchResp.JSON200
		require.NotNil(t, entry)
		assert.Equal(t, created.Uid, entry.Uid)
		assert.Equal(t, uint32(23456), entry.Gid)
		assert.Equal(t, created.Mode, entry.Mode)

		st := getStat(t, filename)
		assert.Equal(t, created.Uid, st.Uid)
		assert.Equal(t, uint32(23456), st.Gid)
		assert.Equal(t, created.Mode, st.Mode)
	})

	t.Run("patching non-existent path returns 404", func(t *testing.T) {
		t.Parallel()

		path := fmt.Sprintf("%s.txt", uuid.NewString())
		resp, err := client.PatchVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.PatchVolumesVolumeIDFileParams{Path: path},
			api.PatchVolumesVolumeIDFileJSONRequestBody{Mode: utils.ToPtr(uint32(0o600))},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode(), string(resp.Body))
	})

	t.Run("can chmod/chown a directory", func(t *testing.T) {
		t.Parallel()

		dirName := uuid.NewString()
		createDir(t, dirName)

		// initial stat
		initial := getStat(t, dirName)
		assert.Equal(t, api.Directory, initial.Type)
		assert.Equal(t, uint32(1000), initial.Uid)
		assert.Equal(t, uint32(1000), initial.Gid)

		resp, err := client.PatchVolumesVolumeIDFileWithResponse(
			t.Context(), volume.VolumeID,
			&api.PatchVolumesVolumeIDFileParams{Path: dirName},
			api.PatchVolumesVolumeIDFileJSONRequestBody{
				Uid:  utils.ToPtr(uint32(1357)),
				Gid:  utils.ToPtr(uint32(2468)),
				Mode: utils.ToPtr(uint32(0o751)),
			},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode(), string(resp.Body))
		st := getStat(t, dirName)
		assert.Equal(t, api.Directory, st.Type)
		assert.Equal(t, uint32(1357), st.Uid)
		assert.Equal(t, uint32(2468), st.Gid)
		assert.Equal(t, uint32(0o751), st.Mode&uint32(os.ModePerm))
	})
}
