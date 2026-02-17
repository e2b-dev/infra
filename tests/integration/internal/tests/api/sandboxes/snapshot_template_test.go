package sandboxes

import (
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
		c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshot.SnapshotID, setup.WithAPIKey())
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
		// Without a name, the names list should be empty
		assert.Empty(t, snapshot.Names)
	})

	t.Run("create snapshot with name", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		name := "my-snapshot-" + sbx.SandboxID
		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, &name)

		assert.NotEmpty(t, snapshot.SnapshotID)
		require.NotEmpty(t, snapshot.Names)
		assert.Contains(t, snapshot.Names[0], name)

		// Creating again with the same name should reuse the same template
		resp2 := createSnapshotTemplate(t, c, sbx.SandboxID, &name)
		require.Equal(t, http.StatusCreated, resp2.StatusCode())
		require.NotNil(t, resp2.JSON201)
		assert.Equal(t, snapshot.SnapshotID, resp2.JSON201.SnapshotID, "Same name should return the same snapshot template ID")
		assert.Equal(t, snapshot.Names, resp2.JSON201.Names, "Same name should return the same names")
	})

	t.Run("create snapshot with name and tag", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		nameV1 := "tagged-snap-" + sbx.SandboxID + ":v1"
		snapshot := createSnapshotTemplateWithCleanup(t, c, sbx.SandboxID, &nameV1)
		require.NotEmpty(t, snapshot.Names)
		assert.Contains(t, snapshot.Names[0], ":v1")

		// Same alias with different tag should reuse the template
		nameV2 := "tagged-snap-" + sbx.SandboxID + ":v2"
		resp2 := createSnapshotTemplate(t, c, sbx.SandboxID, &nameV2)
		require.Equal(t, http.StatusCreated, resp2.StatusCode())
		require.NotNil(t, resp2.JSON201)
		assert.Equal(t, snapshot.SnapshotID, resp2.JSON201.SnapshotID, "Same alias with different tag should reuse the same template")
		require.NotEmpty(t, resp2.JSON201.Names)
		assert.Contains(t, resp2.JSON201.Names[0], ":v2")
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

		deleteResp, err := c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshot.SnapshotID, setup.WithAPIKey())
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

		require.NotEmpty(t, snapshot.Names)
		deleteResp, err := c.DeleteTemplatesTemplateIDWithResponse(t.Context(), snapshot.Names[0], setup.WithAPIKey())
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
				TemplateID: snapshot.SnapshotID,
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

		require.NotEmpty(t, snapshot.Names)
		createResp, err := c.PostSandboxesWithResponse(
			t.Context(),
			api.PostSandboxesJSONRequestBody{
				TemplateID: snapshot.Names[0],
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

// waitForSnapshotting polls the database until a build with status 'snapshotting'
// appears for the given sandbox, or the timeout expires.
func waitForSnapshotting(t *testing.T, db *setup.Database, sandboxID string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var count int

		err := db.Db.TestsRawSQLQuery(t.Context(),
			`SELECT COUNT(*) FROM env_builds eb
			 JOIN env_build_assignments eba ON eba.build_id = eb.id
			 JOIN snapshots s ON s.env_id = eba.env_id
			 WHERE s.sandbox_id = $1 AND eb.status = 'snapshotting'`,
			func(rows pgx.Rows) error {
				if rows.Next() {
					return rows.Scan(&count)
				}

				return nil
			},
			sandboxID,
		)
		require.NoError(t, err)

		if count > 0 {
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for sandbox %s to enter snapshotting state", sandboxID)
}

// startSnapshotInBackground fires a snapshot creation request in a goroutine.
// Returns a channel that closes when the request completes.
func startSnapshotInBackground(t *testing.T, c *api.ClientWithResponses, sandboxID string) <-chan struct{} {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		resp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(), sandboxID,
			api.PostSandboxesSandboxIDSnapshotsJSONRequestBody{},
			setup.WithAPIKey(),
		)
		if err != nil {
			t.Logf("snapshot request error: %v", err)

			return
		}

		t.Logf("snapshot response: %d", resp.StatusCode())

		if resp.StatusCode() == http.StatusCreated && resp.JSON201 != nil {
			t.Cleanup(func() {
				c.DeleteTemplatesTemplateIDWithResponse(t.Context(), resp.JSON201.SnapshotID, setup.WithAPIKey())
			})
		}
	}()

	return done
}

func TestSnapshotTemplateConcurrentOperations(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	db := setup.GetTestDBClient(t)

	t.Run("pause during snapshot creation", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		snapshotDone := startSnapshotInBackground(t, c, sbx.SandboxID)
		waitForSnapshotting(t, db, sbx.SandboxID)

		pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, pauseResp.StatusCode(),
			"pause during snapshotting should wait and succeed, body: %s", string(pauseResp.Body))

		<-snapshotDone
	})

	t.Run("kill during snapshot creation", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		snapshotDone := startSnapshotInBackground(t, c, sbx.SandboxID)
		waitForSnapshotting(t, db, sbx.SandboxID)

		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		// Kill waits for the snapshot to complete, then proceeds normally.
		assert.Equal(t, http.StatusNoContent, killResp.StatusCode(),
			"kill during snapshotting should succeed after waiting, body: %s", string(killResp.Body))

		<-snapshotDone
	})

	t.Run("resume during snapshot creation", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		snapshotDone := startSnapshotInBackground(t, c, sbx.SandboxID)
		waitForSnapshotting(t, db, sbx.SandboxID)

		resumeResp, err := c.PostSandboxesSandboxIDResumeWithResponse(
			t.Context(),
			sbx.SandboxID,
			api.PostSandboxesSandboxIDResumeJSONRequestBody{},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		assert.Equal(t, http.StatusConflict, resumeResp.StatusCode(),
			"resume during snapshotting should return conflict, body: %s", string(resumeResp.Body))

		<-snapshotDone
	})

	t.Run("concurrent snapshot returns conflict", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		snapshotDone := startSnapshotInBackground(t, c, sbx.SandboxID)
		waitForSnapshotting(t, db, sbx.SandboxID)

		resp, err := c.PostSandboxesSandboxIDSnapshotsWithResponse(
			t.Context(),
			sbx.SandboxID,
			api.PostSandboxesSandboxIDSnapshotsJSONRequestBody{},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		assert.Equal(t, http.StatusConflict, resp.StatusCode(),
			"second snapshot during snapshotting should return conflict, body: %s", string(resp.Body))

		<-snapshotDone
	})
}
