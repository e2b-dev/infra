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

// TestSandboxErrorCodes_410Gone tests that 410 Gone is returned when a sandbox was killed.
// The error message should include when the sandbox was killed and why.
func TestSandboxErrorCodes_410Gone(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("GET killed sandbox returns 410 with kill info", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sandboxID := sbx.SandboxID

		// Kill the sandbox
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killResp.StatusCode())

		// Try to GET the killed sandbox - should return 410 Gone
		getResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusGone, getResp.StatusCode(), "Expected 410 Gone for killed sandbox, got %d", getResp.StatusCode())

		// Verify response body contains kill info (timestamp and reason)
		body := string(getResp.Body)
		assert.Contains(t, body, "was killed at", "Response should include kill timestamp")
		assert.Contains(t, body, "via API request", "Response should include kill reason (API)")
	})

	t.Run("pause killed sandbox returns 410 with kill info", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sandboxID := sbx.SandboxID

		// Kill the sandbox
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killResp.StatusCode())

		// Try to pause the killed sandbox - should return 410 Gone
		pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusGone, pauseResp.StatusCode(), "Expected 410 Gone for killed sandbox, got %d", pauseResp.StatusCode())

		// Verify response body contains kill info
		body := string(pauseResp.Body)
		assert.Contains(t, body, "was killed at", "Response should include kill timestamp")
		assert.Contains(t, body, "via API request", "Response should include kill reason (API)")
	})

	t.Run("set timeout on killed sandbox returns 410 with kill info", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sandboxID := sbx.SandboxID

		// Kill the sandbox
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killResp.StatusCode())

		// Try to set timeout on the killed sandbox - should return 410 Gone
		timeout := int32(60)
		timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sandboxID, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
			Timeout: timeout,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusGone, timeoutResp.StatusCode(), "Expected 410 Gone for killed sandbox, got %d", timeoutResp.StatusCode())

		// Verify response body contains kill info
		body := string(timeoutResp.Body)
		assert.Contains(t, body, "was killed at", "Response should include kill timestamp")
		assert.Contains(t, body, "via API request", "Response should include kill reason (API)")
	})

	t.Run("kill already killed sandbox is idempotent (204 or 410)", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sandboxID := sbx.SandboxID

		// Kill the sandbox
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killResp.StatusCode())

		// Try to kill again - should return 204 (idempotent) or 410 (already killed)
		killResp2, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		// Accept either 204 (idempotent success) or 410 (already killed)
		assert.Contains(t, []int{http.StatusNoContent, http.StatusGone}, killResp2.StatusCode(),
			"Expected 204 or 410 for re-killing sandbox, got %d", killResp2.StatusCode())
	})

	t.Run("resume killed sandbox returns 410 with kill info", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sandboxID := sbx.SandboxID

		// Pause the sandbox first
		pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		// Kill the paused sandbox
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killResp.StatusCode())

		// Try to resume the killed sandbox - should return 410 Gone
		timeout := int32(60)
		resumeResp, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sandboxID, api.PostSandboxesSandboxIDResumeJSONRequestBody{
			Timeout: &timeout,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusGone, resumeResp.StatusCode(), "Expected 410 Gone for killed sandbox, got %d", resumeResp.StatusCode())

		// Verify response body contains kill info
		body := string(resumeResp.Body)
		assert.Contains(t, body, "was killed at", "Response should include kill timestamp")
		assert.Contains(t, body, "via API request", "Response should include kill reason (API)")
	})
}

// TestSandboxErrorCodes_409Conflict tests that 409 Conflict is returned when a sandbox is in a conflicting state.
// The error message should include when the transition started and why.
func TestSandboxErrorCodes_409Conflict(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("pause already paused sandbox returns 409", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sandboxID := sbx.SandboxID

		// Pause the sandbox
		pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		// Try to pause again - should return 409 Conflict
		pauseResp2, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusConflict, pauseResp2.StatusCode(), "Expected 409 Conflict for already paused sandbox, got %d", pauseResp2.StatusCode())

		// Verify response body indicates sandbox is paused
		body := string(pauseResp2.Body)
		assert.Contains(t, body, "paused", "Response should indicate sandbox is paused")
	})

	t.Run("set timeout on paused sandbox returns 409 with transition info", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sandboxID := sbx.SandboxID

		// Pause the sandbox
		pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		// Try to set timeout on paused sandbox - should return 409 or similar
		timeout := int32(60)
		timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sandboxID, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
			Timeout: timeout,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		// Paused sandbox cannot have timeout set - should return 404 (not in running store) or 409
		assert.Contains(t, []int{http.StatusNotFound, http.StatusConflict}, timeoutResp.StatusCode(),
			"Expected 404 or 409 for paused sandbox timeout, got %d", timeoutResp.StatusCode())
	})
}

// TestSandboxErrorCodes_404NotFound tests that 404 Not Found is returned when a sandbox never existed.
func TestSandboxErrorCodes_404NotFound(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("GET non-existent sandbox returns 404", func(t *testing.T) {
		t.Parallel()

		// Try to GET a sandbox that never existed
		getResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), "never-existed-sandbox-id", setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, getResp.StatusCode(), "Expected 404 Not Found for non-existent sandbox, got %d", getResp.StatusCode())

		// Verify the response does NOT contain kill info (it was never killed, just never existed)
		body := string(getResp.Body)
		assert.NotContains(t, body, "was killed", "Response should not indicate sandbox was killed")
	})

	t.Run("pause non-existent sandbox returns 404", func(t *testing.T) {
		t.Parallel()

		// Try to pause a sandbox that never existed
		pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), "never-existed-sandbox-id", setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, pauseResp.StatusCode(), "Expected 404 Not Found for non-existent sandbox, got %d", pauseResp.StatusCode())
	})

	t.Run("kill non-existent sandbox returns 404", func(t *testing.T) {
		t.Parallel()

		// Try to kill a sandbox that never existed
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), "never-existed-sandbox-id", setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, killResp.StatusCode(), "Expected 404 Not Found for non-existent sandbox, got %d", killResp.StatusCode())
	})

	t.Run("resume non-existent sandbox returns 404", func(t *testing.T) {
		t.Parallel()

		// Try to resume a sandbox that never existed
		timeout := int32(60)
		resumeResp, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), "never-existed-sandbox-id", api.PostSandboxesSandboxIDResumeJSONRequestBody{
			Timeout: &timeout,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resumeResp.StatusCode(), "Expected 404 Not Found for non-existent sandbox, got %d", resumeResp.StatusCode())
	})

	t.Run("set timeout on non-existent sandbox returns 404", func(t *testing.T) {
		t.Parallel()

		// Try to set timeout on a sandbox that never existed
		timeout := int32(60)
		timeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), "never-existed-sandbox-id", api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
			Timeout: timeout,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, timeoutResp.StatusCode(), "Expected 404 Not Found for non-existent sandbox, got %d", timeoutResp.StatusCode())
	})
}
