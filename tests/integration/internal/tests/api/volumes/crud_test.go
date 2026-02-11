package volumes

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedfs "github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/filesystem"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
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
	assert.NotEmpty(t, createVolume.JSON201.VolumeID)
	volumeID := createVolume.JSON201.VolumeID

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
	assert.Equal(t, volumeID, getVolume.JSON200.VolumeID)

	// list volumes
	listVolumes, err := client.GetVolumesWithResponse(t.Context(), auth...)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listVolumes.StatusCode(), string(listVolumes.Body))
	assert.Contains(t, *listVolumes.JSON200, *getVolume.JSON200)

	// create a sandbox with the volume
	createSandbox, err := client.PostSandboxesWithResponse(
		t.Context(),
		api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
			Timeout:    func() *int32 { v := int32(30); return &v }(),
			Metadata: &api.SandboxMetadata{
				"sandboxType": "test",
			},
			VolumeMounts: &[]api.SandboxVolumeMount{{
				Name: volumeName,
				Path: "/home/user/vol",
			}},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createSandbox.StatusCode(), string(createSandbox.Body))
	require.NotNil(t, createSandbox.JSON201)
	sbx := createSandbox.JSON201

	// ensure mount directory exists (idempotent)
	utils.CreateDir(t, sbx, "vol")

	// write a file
	{
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		utils.UploadFile(t, ctx, sbx, envdClient, "vol/hello.txt", "hello from volume")
	}

	// read the file
	{
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		filePath := "vol/hello.txt"
		readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
			ctx,
			&envd.GetFilesParams{Path: &filePath, Username: sharedutils.ToPtr("user")},
			setup.WithSandbox(sbx.SandboxID),
		)
		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, readRes.StatusCode(), string(readRes.Body))
		assert.Equal(t, "hello from volume", string(readRes.Body))
	}

	// kill the sandbox
	utils.TeardownSandbox(t, client, sbx.SandboxID)

	// start a new sandbox
	createSandbox2, err := client.PostSandboxesWithResponse(
		t.Context(),
		api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
			Timeout:    func() *int32 { v := int32(30); return &v }(),
			Metadata: &api.SandboxMetadata{
				"sandboxType": "test",
			},
			VolumeMounts: &[]api.SandboxVolumeMount{{
				Name: volumeName,
				Path: "/home/user/vol",
			}},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createSandbox2.StatusCode(), string(createSandbox2.Body))
	require.NotNil(t, createSandbox2.JSON201)
	sbx2 := createSandbox2.JSON201

	// read the file
	{
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		filePath := "vol/hello.txt"
		readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
			ctx,
			&envd.GetFilesParams{Path: &filePath, Username: sharedutils.ToPtr("user")},
			setup.WithSandbox(sbx2.SandboxID),
		)
		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, readRes.StatusCode(), string(readRes.Body))
		assert.Equal(t, "hello from volume", string(readRes.Body))
	}

	// delete the file
	{
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		req := connect.NewRequest(&sharedfs.RemoveRequest{Path: "vol/hello.txt"})
		setup.SetSandboxHeader(req.Header(), sbx2.SandboxID)
		setup.SetUserHeader(req.Header(), "user")
		_, remErr := envdClient.FilesystemClient.Remove(ctx, req)
		require.NoError(t, remErr)

		// verify it's gone
		filePath := "vol/hello.txt"
		readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
			ctx,
			&envd.GetFilesParams{Path: &filePath, Username: sharedutils.ToPtr("user")},
			setup.WithSandbox(sbx2.SandboxID),
		)
		require.NoError(t, readErr)
		assert.Equal(t, http.StatusNotFound, readRes.StatusCode(), string(readRes.Body))
	}

	// kill the sandbox
	utils.TeardownSandbox(t, client, sbx2.SandboxID)

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
