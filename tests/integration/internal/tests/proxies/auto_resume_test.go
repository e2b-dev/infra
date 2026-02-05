package api

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestProxyAutoResumePolicies(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	proxyURL, err := url.Parse(setup.ClientProxy)
	require.NoError(t, err)

	client := &http.Client{Timeout: 60 * time.Second}

	authCases := []struct {
		name    string
		headers http.Header
	}{
		{name: "unauthed"},
		{name: "api-key", headers: http.Header{"X-API-Key": []string{setup.APIKey}}},
		{name: "access-token", headers: http.Header{"Authorization": []string{fmt.Sprintf("Bearer %s", setup.AccessToken)}}},
	}

	policies := []struct {
		name   string
		policy string
	}{
		{name: "any", policy: "any"},
		{name: "null", policy: "null"},
	}

	for _, policy := range policies {
		t.Run(policy.name, func(t *testing.T) {
			t.Parallel()

			for _, authCase := range authCases {
				t.Run(authCase.name, func(t *testing.T) {
					t.Parallel()

					options := []utils.SandboxOption{}
					if policy.policy != "null" {
						options = append(options, utils.WithAutoResume(api.NewSandboxAutoResume(policy.policy)))
					}
					sbx := utils.SetupSandboxWithCleanup(t, c, options...)

					ensureSandboxPaused(t, c, sbx.SandboxID)

					resp := proxyRequest(t, client, sbx, proxyURL, authCase.headers)
					require.NoError(t, resp.Body.Close())

					if policy.policy == "any" {
						require.NotEqual(t, http.StatusConflict, resp.StatusCode)
						if resp.StatusCode >= http.StatusInternalServerError {
							require.Equal(t, http.StatusBadGateway, resp.StatusCode)
						}
						waitForSandboxState(t, c, sbx.SandboxID, api.Running)

						return
					}

					require.Equal(t, http.StatusConflict, resp.StatusCode)
					waitForSandboxState(t, c, sbx.SandboxID, api.Paused)
				})
			}
		})
	}
}

func TestProxyAutoResumeConcurrent(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	proxyURL, err := url.Parse(setup.ClientProxy)
	require.NoError(t, err)

	client := &http.Client{Timeout: 60 * time.Second}

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoResume(api.NewSandboxAutoResume("any")))

	ensureSandboxPaused(t, c, sbx.SandboxID)
	waitForSandboxStateWithin(t, c, sbx.SandboxID, api.Paused, 5*time.Second)

	headers := http.Header{"X-API-Key": []string{setup.APIKey}}
	group := errgroup.Group{}
	for range 5 {
		group.Go(func() error {
			req := utils.NewRequest(sbx, proxyURL, 8080, &headers)
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer func() {
				_ = resp.Body.Close()
			}()

			if resp.StatusCode == http.StatusConflict {
				return fmt.Errorf("unexpected conflict for auto-resume request: status=%d sandbox=%s", resp.StatusCode, sbx.SandboxID)
			}
			if resp.StatusCode >= http.StatusInternalServerError && resp.StatusCode != http.StatusBadGateway {
				return fmt.Errorf("unexpected 5xx status for auto-resume request: %d sandbox=%s", resp.StatusCode, sbx.SandboxID)
			}

			return nil
		})
	}

	require.NoError(t, group.Wait())
	waitForSandboxState(t, c, sbx.SandboxID, api.Running)
}

func TestProxyAutoResumeSmoke(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	proxyURL, err := url.Parse(setup.ClientProxy)
	require.NoError(t, err)

	client := &http.Client{Timeout: 60 * time.Second}

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoResume(api.NewSandboxAutoResume("any")))

	ensureSandboxPaused(t, c, sbx.SandboxID)
	waitForSandboxStateWithin(t, c, sbx.SandboxID, api.Paused, 5*time.Second)

	headers := http.Header{"X-API-Key": []string{setup.APIKey}}
	resp := proxyRequest(t, client, sbx, proxyURL, headers)
	require.NoError(t, resp.Body.Close())

	require.NotEqual(t, http.StatusConflict, resp.StatusCode)
	waitForSandboxState(t, c, sbx.SandboxID, api.Running)
}

func proxyRequest(t *testing.T, client *http.Client, sbx *api.Sandbox, proxyURL *url.URL, headers http.Header) *http.Response {
	t.Helper()

	var extraHeaders *http.Header
	if len(headers) > 0 {
		extraHeaders = &headers
	}

	req := utils.NewRequest(sbx, proxyURL, 8080, extraHeaders)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	return resp
}

func ensureSandboxPaused(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	t.Helper()

	state := getSandboxState(t, c, sandboxID)
	if state == api.Paused {
		return
	}

	resp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Contains(t, []int{http.StatusNoContent, http.StatusConflict}, resp.StatusCode())

	waitForSandboxState(t, c, sandboxID, api.Paused)
}

func waitForSandboxState(t *testing.T, c *api.ClientWithResponses, sandboxID string, expected api.SandboxState) {
	t.Helper()

	waitForSandboxStateWithin(t, c, sandboxID, expected, 60*time.Second)
}

func waitForSandboxStateWithin(t *testing.T, c *api.ClientWithResponses, sandboxID string, expected api.SandboxState, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := getSandboxState(t, c, sandboxID)
		if state == expected {
			return
		}

		time.Sleep(250 * time.Millisecond)
	}

	require.Failf(t, "sandbox state mismatch", "sandbox %s did not reach %s in %s", sandboxID, expected, timeout)
}

func getSandboxState(t *testing.T, c *api.ClientWithResponses, sandboxID string) api.SandboxState {
	t.Helper()

	resp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
	require.NotNil(t, resp.JSON200)

	return resp.JSON200.State
}
