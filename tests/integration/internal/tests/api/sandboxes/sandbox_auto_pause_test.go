package sandboxes

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxAutoPausePauseResume(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
	sbxId := sbx.SandboxID

	// Pause the sandbox
	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

	// Resume the sandbox with auto-pause enabled
	_, err = c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	// Set timeout to 0 to force sandbox to be stopped
	resp, err := c.PostSandboxesSandboxIDTimeout(t.Context(), sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)

		return res.StatusCode() == http.StatusOK && res.JSON200 != nil && res.JSON200.State == api.Paused
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox again to check if it resumes correctly
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, sbxResume.StatusCode())
	require.NotNil(t, sbxResume.JSON201)
	assert.Equal(t, sbxResume.JSON201.SandboxID, sbxId)
}

func TestSandboxAutoPauseResumePersisted(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	// Create a sandbox with auto-pause disabled
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
	sbxId := sbx.SandboxID

	envdClient := setup.GetEnvdClient(t, t.Context())
	path := "/test.txt"
	content := "Hello, World!"
	utils.UploadFile(t, t.Context(), sbx, envdClient, path, content)

	// Set timeout to 0 to force sandbox to be stopped
	resp, err := c.PostSandboxesSandboxIDTimeout(t.Context(), sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)

		return res.JSON200.State == api.Paused
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not paused")

	// Resume the sandbox with auto-pause enabled
	_, err = c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	// Check if the file is still there after resuming
	fileResponse, err := envdClient.HTTPClient.GetFilesWithResponse(
		t.Context(),
		&envd.GetFilesParams{
			Path:     &path,
			Username: sharedUtils.ToPtr("user"),
		},
		setup.WithSandbox(sbxId),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, fileResponse.StatusCode())
	require.Equal(t, content, string(fileResponse.Body))

	content = "Hello, E2B!"
	utils.UploadFile(t, t.Context(), sbx, envdClient, path, content)

	// Set timeout to 0 to force sandbox to be stopped
	resp, err = c.PostSandboxesSandboxIDTimeout(t.Context(), sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)

		return res.JSON200.State == api.Paused
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not paused")

	// Resume the sandbox again to check if it resumes correctly
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	require.Equal(t, http.StatusCreated, sbxResume.StatusCode())
	require.NotNil(t, sbxResume.JSON201)
	assert.Equal(t, sbxResume.JSON201.SandboxID, sbxId)

	// Check if the file is still there after resuming
	fileResponse, err = envdClient.HTTPClient.GetFilesWithResponse(
		t.Context(),
		&envd.GetFilesParams{
			Path:     &path,
			Username: sharedUtils.ToPtr("user"),
		},
		setup.WithSandbox(sbxId),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, fileResponse.StatusCode())
	assert.Equal(t, content, string(fileResponse.Body))
}

func TestSandboxNotAutoPause(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	// Create a sandbox with auto-pause disabled
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
	sbxId := sbx.SandboxID

	// Set timeout to 0 to force sandbox to be stopped
	resp, err := c.PostSandboxesSandboxIDTimeout(t.Context(), sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)

		return res.StatusCode() == http.StatusNotFound
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox with auto-pause enabled
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, sbxResume.StatusCode())
}

func TestSandboxTimeoutPersistsAcrossPauseResume(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	const shortTTL int32 = 5
	const longTTL int32 = 120

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true), utils.WithTimeout(shortTTL))
	sbxId := sbx.SandboxID
	db := setup.GetTestDBClient(t)

	waitForPaused := func(timeout time.Duration) {
		t.Helper()
		require.Eventually(t, func() bool {
			res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
			require.NoError(t, err)
			if res.StatusCode() == http.StatusNotFound {
				// Snapshot may not be ready yet, keep waiting.
				return false
			}
			require.Equal(t, http.StatusOK, res.StatusCode())
			require.NotNil(t, res.JSON200)

			return res.JSON200.State == api.Paused
		}, timeout, 20*time.Millisecond, "Sandbox is not paused")
	}

	resumeAndAssertTTL := func(expected int32) {
		t.Helper()

		var resumeResp *api.PostSandboxesSandboxIDResumeResponse
		require.Eventually(t, func() bool {
			resp, err := c.PostSandboxesSandboxIDResumeWithResponse(
				t.Context(),
				sbxId,
				api.PostSandboxesSandboxIDResumeJSONRequestBody{},
				setup.WithAPIKey(),
			)
			require.NoError(t, err)
			if resp.StatusCode() == http.StatusNotFound {
				// Snapshot might not be ready yet after pausing.
				return false
			}
			require.Equal(t, http.StatusCreated, resp.StatusCode())
			require.NotNil(t, resp.JSON201)
			resumeResp = resp

			return true
		}, 20*time.Second, 50*time.Millisecond, "sandbox did not resume")
		require.NotNil(t, resumeResp)

		detailResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, detailResp.StatusCode())
		require.NotNil(t, detailResp.JSON200)

		actual := int32(detailResp.JSON200.EndAt.Sub(detailResp.JSON200.StartedAt).Seconds())
		assert.InDelta(t, expected, actual, 2, "sandbox TTL should persist across resume")
	}

	// Short TTL: allow auto-pause to happen naturally, resume, TTL should stay short.
		waitForPaused(30 * time.Second)
		if snap, err := db.Db.GetLastSnapshot(t.Context(), sbxId); err == nil && snap.Snapshot.Config != nil {
			t.Logf("snapshot config after pause: %+v", *snap.Snapshot.Config)
		}
		resumeAndAssertTTL(shortTTL)

	// Short TTL again: allow auto-pause again and ensure it still resumes with short TTL.
		waitForPaused(30 * time.Second)
		if snap, err := db.Db.GetLastSnapshot(t.Context(), sbxId); err == nil && snap.Snapshot.Config != nil {
			t.Logf("snapshot config after pause: %+v", *snap.Snapshot.Config)
		}
		resumeAndAssertTTL(shortTTL)

	// Long TTL: set a longer timeout, manually pause, resume, TTL should stay long.
	setTimeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: longTTL,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, setTimeoutResp.StatusCode())

	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		waitForPaused(30 * time.Second)
		if snap, err := db.Db.GetLastSnapshot(t.Context(), sbxId); err == nil && snap.Snapshot.Config != nil {
			t.Logf("snapshot config after pause: %+v", *snap.Snapshot.Config)
		}
		resumeAndAssertTTL(longTTL)
}
