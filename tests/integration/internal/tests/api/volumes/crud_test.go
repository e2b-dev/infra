package volumes

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	// create volume
	createVolume, err := client.PostVolumesWithResponse(
		t.Context(),
		api.PostVolumesJSONRequestBody{Name: volumeName},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createVolume.StatusCode(), string(createVolume.Body))
	assert.Equal(t, volumeName, createVolume.JSON201.Name)
	assert.NotEmpty(t, createVolume.JSON201.VolumeID)
	volume := createVolume.JSON201

	// fail to create volume again
	createVolumeFailure, err := client.PostVolumesWithResponse(
		t.Context(),
		api.PostVolumesJSONRequestBody{Name: volumeName},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, createVolumeFailure.StatusCode(), string(createVolumeFailure.Body))

	// retrieve volume
	getVolume, err := client.GetVolumesVolumeIDWithResponse(t.Context(), volume.VolumeID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getVolume.StatusCode(), string(getVolume.Body))
	assert.Equal(t, *volume, *getVolume.JSON200)

	// list volumes
	listVolumes, err := client.GetVolumesWithResponse(t.Context(), setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listVolumes.StatusCode(), string(listVolumes.Body))
	assert.Contains(t, *listVolumes.JSON200, *getVolume.JSON200)

	// paths
	volumeMountPath := "/home/user/vol"
	filePath := filepath.Join(volumeMountPath, "hello.txt")

	// create a sandbox with the volume
	timeout := int32(30)
	createSandbox, err := client.PostSandboxesWithResponse(
		t.Context(),
		api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
			Timeout:    &timeout,
			Metadata: &api.SandboxMetadata{
				"sandboxType": "test",
			},
			VolumeMounts: &[]api.SandboxVolumeMount{{
				Name: volumeName,
				Path: volumeMountPath,
			}},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createSandbox.StatusCode(), string(createSandbox.Body))
	require.NotNil(t, createSandbox.JSON201)
	sbx := createSandbox.JSON201

	// verify that path is mounted
	envdClient := setup.GetEnvdClient(t, t.Context())
	require.Eventually(t, func() bool {
		output, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "mount")
		if err != nil {
			t.Logf("error running mount command: %v", err)

			return false
		}

		return strings.Contains(output, volumeMountPath)
	}, 10*time.Second, 1*time.Second)

	// write a file
	{
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		utils.UploadFile(t, ctx, sbx, envdClient, filePath, "hello from volume")
	}

	// read the file
	{
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
			ctx,
			&envd.GetFilesParams{Path: &filePath, Username: sharedutils.ToPtr("user")},
			setup.WithSandbox(t, sbx.SandboxID),
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
			Timeout: func() *int32 {
				v := int32(30)

				return &v
			}(),
			Metadata: &api.SandboxMetadata{
				"sandboxType": "test",
			},
			VolumeMounts: &[]api.SandboxVolumeMount{{
				Name: volumeName,
				Path: volumeMountPath,
			}},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, createSandbox2.StatusCode(), string(createSandbox2.Body))
	require.NotNil(t, createSandbox2.JSON201)
	sbx2 := createSandbox2.JSON201

	// verify that path is mounted
	require.Eventually(t, func() bool {
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		output, err := utils.ExecCommandAsRootWithOutput(t, ctx, sbx2, envdClient, "mount")
		if err != nil {
			t.Logf("error running mount command: %v", err)

			return false
		}

		return strings.Contains(output, volumeMountPath)
	}, 10*time.Second, 1*time.Second)

	// read the file
	{
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
			ctx,
			&envd.GetFilesParams{Path: &filePath, Username: sharedutils.ToPtr("user")},
			setup.WithSandbox(t, sbx2.SandboxID),
		)
		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, readRes.StatusCode(), string(readRes.Body))
		assert.Equal(t, "hello from volume", string(readRes.Body))
	}

	// delete the file
	{
		ctx := t.Context()
		envdClient := setup.GetEnvdClient(t, ctx)
		req := connect.NewRequest(&sharedfs.RemoveRequest{Path: filePath})
		setup.SetSandboxHeader(t, req.Header(), sbx2.SandboxID)
		setup.SetUserHeader(t, req.Header(), "user")
		_, remErr := envdClient.FilesystemClient.Remove(ctx, req)
		require.NoError(t, remErr)

		// verify it's gone
		readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
			ctx,
			&envd.GetFilesParams{Path: &filePath, Username: sharedutils.ToPtr("user")},
			setup.WithSandbox(t, sbx2.SandboxID),
		)
		require.NoError(t, readErr)
		assert.Equal(t, http.StatusNotFound, readRes.StatusCode(), string(readRes.Body))
	}

	// kill the sandbox
	utils.TeardownSandbox(t, client, sbx2.SandboxID)

	// delete volume
	deleteVolume, err := client.DeleteVolumesVolumeIDWithResponse(t.Context(), volume.VolumeID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, deleteVolume.StatusCode(), string(deleteVolume.Body))

	// verify volume is deleted
	getVolumeFailed, err := client.GetVolumesVolumeIDWithResponse(t.Context(), volume.VolumeID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, getVolumeFailed.StatusCode(), string(getVolumeFailed.Body))

	// verify volume is not in list
	listVolumes, err = client.GetVolumesWithResponse(t.Context(), setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listVolumes.StatusCode(), string(listVolumes.Body))
	assert.NotContains(t, *listVolumes.JSON200, *getVolume.JSON200)

	// verify volume cannot be deleted again
	deleteVolume, err = client.DeleteVolumesVolumeIDWithResponse(t.Context(), volume.VolumeID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, deleteVolume.StatusCode(), string(deleteVolume.Body))
}
