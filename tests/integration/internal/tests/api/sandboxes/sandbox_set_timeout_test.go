package sandboxes

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxSetTimeoutPausingSandbox(t *testing.T) {
	c := setup.GetAPIClient()

	t.Run("test set timeout while pausing", func(t *testing.T) {
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
		sbxId := sbx.SandboxID

		// Pause the sandbox
		wg := errgroup.Group{}
		wg.Go(func() error {
			pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
			if err != nil {
				return err
			}

			if pauseResp.StatusCode() != http.StatusNoContent {
				return fmt.Errorf("unexpected status code: %d", pauseResp.StatusCode())
			}

			return nil
		})

		for range 5 {
			time.Sleep(200 * time.Millisecond)
			wg.Go(func() error {
				setTimeoutResp, err := c.PostSandboxesSandboxIDTimeoutWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDTimeoutJSONRequestBody{
					Timeout: 15,
				},
					setup.WithAPIKey())
				if err != nil {
					return err
				}

				if setTimeoutResp.StatusCode() != http.StatusNotFound {
					return fmt.Errorf("unexpected status code: %d", setTimeoutResp.StatusCode())
				}

				return nil
			})
		}

		err := wg.Wait()
		require.NoError(t, err)
	})
}
