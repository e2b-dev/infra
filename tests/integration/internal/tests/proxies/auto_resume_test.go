package api

import (
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxAutoResumeViaProxy(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	ctx := t.Context()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))

	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		if err != nil || res.JSON200 == nil {
			return false
		}

		return res.JSON200.State == api.Paused
	}, 30*time.Second, time.Second, "sandbox did not pause in time")

	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{Timeout: 10 * time.Second}
	port := 3210

	require.Eventually(t, func() bool {
		req := utils.NewRequest(sbx, proxyURL, port, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("proxy request error: %v", err)

			return false
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusBadGateway {
			return true
		}

		body, _ := io.ReadAll(resp.Body)
		t.Logf("proxy status=%d body=%s", resp.StatusCode, string(body))

		return false
	}, 45*time.Second, time.Second, "proxy did not auto-resume paused sandbox")

	require.Eventually(t, func() bool {
		res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		if err != nil || res.JSON200 == nil {
			return false
		}

		return res.JSON200.State == api.Running
	}, 30*time.Second, time.Second, "sandbox did not resume after proxy request")
}
