package sandboxes

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxConnect(t *testing.T) {
	c := setup.GetAPIClient()

	t.Run("connect with paused sandbox", func(t *testing.T) {
		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID
		pauseSandbox(t, c, sbxId)

		// Connect to the sandbox
		sbxConnect, err := c.PostSandboxesSandboxIDConnectWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDConnectJSONRequestBody{
			Timeout: 30,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, sbxConnect.StatusCode())
		require.NotNil(t, sbxConnect.JSON201)
		assert.Equal(t, sbxConnect.JSON201.SandboxID, sbxId)

		// Check if the sandbox is running
		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)
		assert.Equal(t, api.Running, res.JSON200.State)
	})

	t.Run("connect to running sandbox", func(t *testing.T) {
		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c)
		sbxId := sbx.SandboxID

		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)
		assert.Equal(t, api.Running, res.JSON200.State)

		initialEndTime := res.JSON200.EndAt

		// Connect to the sandbox
		sbxConnect, err := c.PostSandboxesSandboxIDConnectWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDConnectJSONRequestBody{
			Timeout: 321,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, sbxConnect.StatusCode())
		require.NotNil(t, sbxConnect.JSON201)
		assert.Equal(t, sbxConnect.JSON201.SandboxID, sbxId)

		// Check if the sandbox is running and the timeout isn't changed
		res, err = c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)
		assert.Equal(t, api.Running, res.JSON200.State)

		assert.Equal(t, initialEndTime, res.JSON200.EndAt, "the timeout shouldn't be changed")
	})

	t.Run("connect to not existing sandbox", func(t *testing.T) {
		// Try to connect the sandbox
		sbxConnect, err := c.PostSandboxesSandboxIDConnectWithResponse(t.Context(), "it-isnt-there", api.PostSandboxesSandboxIDConnectJSONRequestBody{
			Timeout: 30,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, sbxConnect.StatusCode())
	})

	t.Run("concurrent connects - not returning early", func(t *testing.T) {
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
				// Try to connect the sandbox
				sbxConnect, err := c.PostSandboxesSandboxIDConnectWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDConnectJSONRequestBody{
					Timeout: 30,
				}, setup.WithAPIKey())
				if err != nil {
					return fmt.Errorf("connect sandbox - %w", err)
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

				if sbxConnect.StatusCode() != http.StatusCreated && sbxConnect.StatusCode() != http.StatusOK {
					return fmt.Errorf("connect sandbox - unexpected status code: %d", sbxConnect.StatusCode())
				}

				return nil
			})
		}

		err = wg.Wait()
		require.NoError(t, err)
	})
}
