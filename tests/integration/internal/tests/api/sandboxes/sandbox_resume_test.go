package sandboxes

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxResume(t *testing.T) {
	c := setup.GetAPIClient()

	t.Run("regular resume", func(t *testing.T) {
		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		// Set timeout to 0 to force sandbox to be stopped
		resp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)
		assert.Equal(t, api.Paused, res.JSON200.State)

		// Resume the sandbox with auto-pause enabled
		sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, sbxResume.StatusCode())
		require.NotNil(t, sbxResume.JSON201)
		assert.Equal(t, sbxResume.JSON201.SandboxID, sbxId)
	})

	t.Run("concurrent resumes", func(t *testing.T) {
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
		sbxId := sbx.SandboxID

		pauseSandbox(t, c, sbxId)

		// Pause the sandbox
		wg := errgroup.Group{}
		resumed := atomic.Bool{}
		for range 5 {
			wg.Go(func() error {
				resumeResp, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
				require.NoError(t, err)
				require.Contains(t, []int{http.StatusCreated, http.StatusConflict}, resumeResp.StatusCode())

				if resumeResp.StatusCode() == http.StatusCreated {
					resumed.Store(true)
				}

				return nil
			})
		}

		err := wg.Wait()
		require.NoError(t, err)

		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)
		assert.Equal(t, api.Running, res.JSON200.State)

		assert.True(t, resumed.Load(), "at least one resume should succeed")
	})

	t.Run("resume killed sandbox", func(t *testing.T) {
		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
		sbxId := sbx.SandboxID

		// Kill the sandbox
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killResp.StatusCode())

		// Try to resume the sandbox
		sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, sbxResume.StatusCode())
	})

	t.Run("resume killed sandbox", func(t *testing.T) {
		c := setup.GetAPIClient()

		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
		sbxId := sbx.SandboxID

		// Pause the sandbox
		resp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		// Kill the sandbox
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killResp.StatusCode())

		// Try to kill the sandbox
		sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, sbxResume.StatusCode())
	})

	t.Run("concurrent resumes - not returning early", func(t *testing.T) {
		c := setup.GetAPIClient()

		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c)
		sbxId := sbx.SandboxID

		// Pause the sandbox
		resp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		wg := errgroup.Group{}
		for range 5 {
			wg.Go(func() error {
				// Try to resume the sandbox
				sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
				if err != nil {
					return fmt.Errorf("resume sandbox - %w", err)
				}

				// Try to check the status of the sandbox
				sbxState, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
				if err != nil {
					return fmt.Errorf("get sandbox - %w", err)
				}

				if sbxState.StatusCode() != http.StatusOK {
					return fmt.Errorf("get sandbox - unexpected status code: %d", sbxState.StatusCode())
				}

				if sbxState.JSON200.State != api.Running {
					return fmt.Errorf("get sandbox - unexpected state: %s", sbxState.JSON200.State)
				}

				if sbxResume.StatusCode() != http.StatusCreated {
					return fmt.Errorf("resume sandbox - unexpected status code: %d", sbxResume.StatusCode())
				}

				return nil
			})
		}

		err = wg.Wait()
		require.NoError(t, err)
	})
}
