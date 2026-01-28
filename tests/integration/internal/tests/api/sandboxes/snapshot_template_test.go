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

func createSnapshotTemplate(t *testing.T, c *api.ClientWithResponses, sbxId string, name *string) *api.PostSandboxesSandboxIDSnapshotsResponse {
	t.Helper()

	resp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
		t.Context(),
		sbxId,
		api.PostSandboxesSandboxIDSnapshotsJSONRequestBody{Name: name},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)

	return resp
}

func createSnapshotTemplateWithCleanup(t *testing.T, c *api.ClientWithResponses, sbxId string, name *string) *api.SnapshotInfo {
	t.Helper()

	resp := createSnapshotTemplate(t, c, sbxId, name)
	require.Equal(t, http.StatusCreated, resp.StatusCode())
	require.NotNil(t, resp.JSON201)

	snapshot := resp.JSON201
	t.Cleanup(func() {
		c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshot.Name, setup.WithAPIKey())
	})

	return snapshot
}

func TestSnapshotTemplateCreate(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("create snapshot without name", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, nil)

		assert.NotEmpty(t, snapshot.SnapshotID)
		assert.NotEmpty(t, snapshot.Name)
		// Without a name, the name falls back to the template ID
		assert.Equal(t, snapshot.SnapshotID, snapshot.Name)
	})

	t.Run("create snapshot with name", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		name := "my-snapshot-" + sbx.SandboxID
		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, &name)

		assert.NotEmpty(t, snapshot.SnapshotID)
		assert.Contains(t, snapshot.Name, name)

		// Creating again with the same name should reuse the same template
		resp2 := createSnapshotTemplate(t, c, sbx.SandboxID, &name)
		require.Equal(t, http.StatusCreated, resp2.StatusCode())
		require.NotNil(t, resp2.JSON201)
		assert.Equal(t, snapshot.SnapshotID, resp2.JSON201.SnapshotID, "Same name should return the same snapshot template ID")
		assert.Equal(t, snapshot.Name, resp2.JSON201.Name, "Same name should return the same name")
	})

	t.Run("create snapshot with name and tag", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		nameV1 := "tagged-snap-" + sbx.SandboxID + ":v1"
		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, &nameV1)
		assert.Contains(t, snapshot.Name, ":v1")

		// Same alias with different tag should reuse the template
		nameV2 := "tagged-snap-" + sbx.SandboxID + ":v2"
		resp2 := createSnapshotTemplate(t, c, sbx.SandboxID, &nameV2)
		require.Equal(t, http.StatusCreated, resp2.StatusCode())
		require.NotNil(t, resp2.JSON201)
		assert.Equal(t, snapshot.SnapshotID, resp2.JSON201.SnapshotID, "Same alias with different tag should reuse the same template")
		assert.Contains(t, resp2.JSON201.Name, ":v2")
	})

	t.Run("create snapshot for non-existent sandbox", func(t *testing.T) {
		t.Parallel()
		resp := createSnapshotTemplate(t, c, "non-existent-sandbox", nil)
		require.Equal(t, http.StatusNotFound, resp.StatusCode())
	})
}

func TestSnapshotTemplateList(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("list snapshots", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, nil)

		listResp, err := c.GetSnapshotsWithResponse(t.Context(), &api.GetSnapshotsParams{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())
		require.NotNil(t, listResp.JSON200)

		found := false
		for _, snap := range *listResp.JSON200 {
			if snap.SnapshotID == snapshot.SnapshotID {
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

		createSnapshotTemplateWithCleanup(t, c, sbxId, nil)

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

func TestSnapshotTemplateDelete(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("delete unnamed snapshot by name", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, nil)

		deleteResp, err := c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshot.Name, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, deleteResp.StatusCode())

		listResp, err := c.GetSnapshotsWithResponse(t.Context(), &api.GetSnapshotsParams{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())

		for _, snap := range *listResp.JSON200 {
			assert.NotEqual(t, snapshot.SnapshotID, snap.SnapshotID, "Deleted snapshot should not appear in list")
		}
	})

	t.Run("delete named snapshot by name", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		name := "del-snap-" + sbx.SandboxID
		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, &name)

		deleteResp, err := c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshot.Name, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, deleteResp.StatusCode())

		listResp, err := c.GetSnapshotsWithResponse(t.Context(), &api.GetSnapshotsParams{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listResp.StatusCode())

		for _, snap := range *listResp.JSON200 {
			assert.NotEqual(t, snapshot.SnapshotID, snap.SnapshotID, "Deleted snapshot should not appear in list")
		}
	})

	t.Run("delete non-existent snapshot", func(t *testing.T) {
		t.Parallel()
		deleteResp, err := c.DeleteTemplatesTemplateIDWithResponse(t.Context(), "non-existent-snapshot", setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, deleteResp.StatusCode())
	})
}

func TestSnapshotTemplateCreateSandbox(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("create sandbox from unnamed snapshot using name", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, nil)

		createResp, err := c.PostSandboxesWithResponse(
			t.Context(),
			api.PostSandboxesJSONRequestBody{
				TemplateID: snapshot.Name,
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

		assert.NotEqual(t, sbx.SandboxID, newSandbox.SandboxID)
		assert.Equal(t, snapshot.SnapshotID, newSandbox.TemplateID)
	})

	t.Run("create sandbox from named snapshot using name", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		name := "sbx-snap-" + sbx.SandboxID
		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, &name)

		createResp, err := c.PostSandboxesWithResponse(
			t.Context(),
			api.PostSandboxesJSONRequestBody{
				TemplateID: snapshot.Name,
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

		assert.NotEqual(t, sbx.SandboxID, newSandbox.SandboxID)
		assert.Equal(t, snapshot.SnapshotID, newSandbox.TemplateID)
	})
}
