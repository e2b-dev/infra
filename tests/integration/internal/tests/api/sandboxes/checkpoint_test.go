package sandboxes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestCheckpointCreate(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("create checkpoint successfully", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		resp, err := c.PostSandboxesSandboxIDCheckpointsWithResponse(
			t.Context(),
			sbxId,
			api.PostSandboxesSandboxIDCheckpointsJSONRequestBody{
				Name: strPtr("test-checkpoint"),
			},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode())
		require.NotNil(t, resp.JSON201)

		checkpoint := resp.JSON201
		assert.NotEmpty(t, checkpoint.CheckpointID)
		assert.Equal(t, sbxId, checkpoint.SandboxID)
		assert.NotNil(t, checkpoint.Name)
		assert.Contains(t, *checkpoint.Name, "test-checkpoint")
		assert.NotZero(t, checkpoint.CreatedAt)

		listResp, err := c.GetSandboxesSandboxIDCheckpointsWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())
		require.NotNil(t, listResp.JSON200)
		require.Len(t, *listResp.JSON200, 1)
	})

	t.Run("create checkpoint without name", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		resp, err := c.PostSandboxesSandboxIDCheckpointsWithResponse(
			t.Context(),
			sbxId,
			api.PostSandboxesSandboxIDCheckpointsJSONRequestBody{},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode())
		require.NotNil(t, resp.JSON201)
		assert.NotEmpty(t, resp.JSON201.CheckpointID)
	})

	t.Run("create multiple checkpoints", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		for i := 0; i < 3; i++ {
			resp, err := c.PostSandboxesSandboxIDCheckpointsWithResponse(
				t.Context(),
				sbxId,
				api.PostSandboxesSandboxIDCheckpointsJSONRequestBody{},
				setup.WithAPIKey(),
			)
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, resp.StatusCode())
		}

		listResp, err := c.GetSandboxesSandboxIDCheckpointsWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())
		require.NotNil(t, listResp.JSON200)
		assert.Len(t, *listResp.JSON200, 3)
	})

	t.Run("create checkpoint for non-existent sandbox", func(t *testing.T) {
		t.Parallel()
		resp, err := c.PostSandboxesSandboxIDCheckpointsWithResponse(
			t.Context(),
			"non-existent-sandbox",
			api.PostSandboxesSandboxIDCheckpointsJSONRequestBody{},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode())
	})
}

func TestCheckpointRestore(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("restore to checkpoint", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		checkpointResp, err := c.PostSandboxesSandboxIDCheckpointsWithResponse(
			t.Context(),
			sbxId,
			api.PostSandboxesSandboxIDCheckpointsJSONRequestBody{
				Name: strPtr("restore-test"),
			},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, checkpointResp.StatusCode())

		checkpointID := checkpointResp.JSON201.CheckpointID

		restoreResp, err := c.PostSandboxesSandboxIDCheckpointsCheckpointIDRestoreWithResponse(
			t.Context(),
			sbxId,
			checkpointID,
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, restoreResp.StatusCode())

		detailResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, detailResp.StatusCode())
		require.NotNil(t, detailResp.JSON200)
		assert.Equal(t, api.Running, detailResp.JSON200.State)
	})

	t.Run("restore to non-existent checkpoint", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		restoreResp, err := c.PostSandboxesSandboxIDCheckpointsCheckpointIDRestoreWithResponse(
			t.Context(),
			sbxId,
			"00000000-0000-0000-0000-000000000000",
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, restoreResp.StatusCode())
	})
}

func TestCheckpointDelete(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("delete checkpoint", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		checkpointResp, err := c.PostSandboxesSandboxIDCheckpointsWithResponse(
			t.Context(),
			sbxId,
			api.PostSandboxesSandboxIDCheckpointsJSONRequestBody{},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, checkpointResp.StatusCode())

		checkpointID := checkpointResp.JSON201.CheckpointID

		deleteResp, err := c.DeleteSandboxesSandboxIDCheckpointsCheckpointIDWithResponse(
			t.Context(),
			sbxId,
			checkpointID,
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, deleteResp.StatusCode())

		listResp, err := c.GetSandboxesSandboxIDCheckpointsWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())
		assert.Empty(t, *listResp.JSON200)
	})

	t.Run("delete non-existent checkpoint", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		deleteResp, err := c.DeleteSandboxesSandboxIDCheckpointsCheckpointIDWithResponse(
			t.Context(),
			sbxId,
			"00000000-0000-0000-0000-000000000000",
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, deleteResp.StatusCode())
	})
}

func strPtr(s string) *string {
	return &s
}
