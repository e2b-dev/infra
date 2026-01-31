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

func TestSnapshotCreate(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("create snapshot successfully", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		resp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(),
			sbxId,
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode())
		require.NotNil(t, resp.JSON201)

		snapshot := resp.JSON201
		assert.NotEmpty(t, snapshot.SnapshotID)
		assert.Contains(t, snapshot.SnapshotID, "snapshot_")

		t.Cleanup(func() {
			c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshot.SnapshotID, setup.WithAPIKey())
		})
	})

	t.Run("create snapshot for non-existent sandbox", func(t *testing.T) {
		t.Parallel()
		resp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(),
			"non-existent-sandbox",
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode())
	})
}

func TestSnapshotList(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("list snapshots", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		createResp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(),
			sbxId,
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, createResp.StatusCode())

		snapshotID := createResp.JSON201.SnapshotID
		t.Cleanup(func() {
			c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshotID, setup.WithAPIKey())
		})

		listResp, err := c.GetSnapshotsWithResponse(t.Context(), &api.GetSnapshotsParams{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())
		require.NotNil(t, listResp.JSON200)

		found := false
		for _, snap := range *listResp.JSON200 {
			if snap.SnapshotID == snapshotID {
				found = true

				break
			}
		}
		assert.True(t, found, "Created snapshot should appear in the list")
	})

	t.Run("list snapshots filtered by sandbox ID", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		createResp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(),
			sbxId,
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, createResp.StatusCode())

		snapshotID := createResp.JSON201.SnapshotID
		t.Cleanup(func() {
			c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshotID, setup.WithAPIKey())
		})

		listResp, err := c.GetSnapshotsWithResponse(t.Context(), &api.GetSnapshotsParams{
			SandboxID: &sbxId,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())
		require.NotNil(t, listResp.JSON200)

		for _, snap := range *listResp.JSON200 {
			assert.NotEmpty(t, snap.SnapshotID)
		}
	})
}

func TestSnapshotGet(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("verify snapshot exists in list", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		createResp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(),
			sbxId,
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, createResp.StatusCode())

		snapshotID := createResp.JSON201.SnapshotID
		t.Cleanup(func() {
			c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshotID, setup.WithAPIKey())
		})

		listResp, err := c.GetSnapshotsWithResponse(t.Context(), &api.GetSnapshotsParams{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())
		require.NotNil(t, listResp.JSON200)

		found := false
		for _, snap := range *listResp.JSON200 {
			if snap.SnapshotID == snapshotID {
				found = true
				assert.Equal(t, snapshotID, snap.SnapshotID)

				break
			}
		}
		assert.True(t, found, "Snapshot should exist in list")
	})
}

func TestSnapshotDelete(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("delete snapshot", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		createResp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(),
			sbxId,
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, createResp.StatusCode())

		snapshotID := createResp.JSON201.SnapshotID

		deleteResp, err := c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshotID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, deleteResp.StatusCode())

		listResp, err := c.GetSnapshotsWithResponse(t.Context(), &api.GetSnapshotsParams{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())

		for _, snap := range *listResp.JSON200 {
			assert.NotEqual(t, snapshotID, snap.SnapshotID, "Deleted snapshot should not appear in list")
		}
	})

	t.Run("delete non-existent snapshot", func(t *testing.T) {
		t.Parallel()
		deleteResp, err := c.DeleteTemplatesTemplateIDWithResponse(t.Context(), "non-existent-snapshot", setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, deleteResp.StatusCode())
	})
}

func TestSnapshotCreateSandbox(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("create sandbox from snapshot", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		snapshotResp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(),
			sbxId,
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, snapshotResp.StatusCode())

		snapshotID := snapshotResp.JSON201.SnapshotID
		t.Cleanup(func() {
			c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshotID, setup.WithAPIKey())
		})

		createResp, err := c.PostSandboxesWithResponse(
			t.Context(),
			api.PostSandboxesJSONRequestBody{
				TemplateID: snapshotID,
			},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, createResp.StatusCode())
		require.NotNil(t, createResp.JSON201)

		newSandbox := createResp.JSON201
		t.Cleanup(func() {
			c.DeleteSandboxesSandboxIDWithResponse(t.Context(), newSandbox.SandboxID, setup.WithAPIKey())
		})

		assert.NotEqual(t, sbxId, newSandbox.SandboxID)
		assert.Equal(t, snapshotID, newSandbox.TemplateID)
	})
}
