package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxAutoPausePauseResume(t *testing.T) {
	c := setup.GetAPIClient()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
	sbxId := sbx.SandboxID

	// Pause the sandbox
	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

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
		return res.StatusCode() == http.StatusOK && res.JSON200 != nil && res.JSON200.State == "paused"
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox again to check if it resumes correctly
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, sbxResume.StatusCode())
	require.NotNil(t, sbxResume.JSON201)
	assert.Equal(t, sbxResume.JSON201.SandboxID, sbxId)
}

func TestSandboxAutoPauseResumePersisted(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox with auto-pause disabled
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
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
		return res.StatusCode() == http.StatusOK && res.JSON200 != nil && res.JSON200.State == "paused"
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox with auto-pause enabled
	_, err = c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	// Set timeout to 0 to force sandbox to be stopped
	resp, err = c.PostSandboxesSandboxIDTimeout(t.Context(), sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		return res.StatusCode() == http.StatusOK && res.JSON200 != nil && res.JSON200.State == "paused"
	}, 10*time.Second, 10*time.Millisecond, "Sandbox is not stopped")

	// Resume the sandbox again to check if it resumes correctly
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, sbxResume.StatusCode())
	require.NotNil(t, sbxResume.JSON201)
	assert.Equal(t, sbxResume.JSON201.SandboxID, sbxId)
}

func TestSandboxNotAutoPause(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox with auto-pause disabled
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
	sbxId := sbx.SandboxID

	// Set timeout to 0 to force sandbox to be stopped
	_, err := c.PostSandboxesSandboxIDTimeout(t.Context(), sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
		Timeout: 0,
	}, setup.WithAPIKey())
	require.NoError(t, err)

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
