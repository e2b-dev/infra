package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxAutoResumeViaExec(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	ctx := t.Context()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true), utils.WithAutoResume(api.Any))
	envdClient := setup.GetEnvdClient(t, ctx)

	// Run ls before pausing.
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "ls")
	require.NoError(t, err)

	// Pause the sandbox.
	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

	res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())
	require.Equal(t, api.Paused, res.JSON200.State)
	// Run ls again — this should trigger auto-resume.
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "ls")
	require.NoError(t, err)

	// Verify the sandbox is running again.
	res, err = c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())
	require.Equal(t, api.Running, res.JSON200.State, "sandbox should be running after auto-resume")
}

func TestSandboxAutoResumeViaProxy(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	ctx := t.Context()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true), utils.WithAutoResume(api.Any))
	envdClient := setup.GetEnvdClient(t, ctx)

	// Start an HTTP server inside the sandbox.
	serverCtx, serverCancel := context.WithCancel(ctx)
	port := 8000
	serverReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "python",
			Args: []string{"-m", "http.server", fmt.Sprintf("%d", port)},
		},
	})
	setup.SetSandboxHeader(serverReq.Header(), sbx.SandboxID)
	setup.SetUserHeader(serverReq.Header(), "user")
	serverStream, err := envdClient.ProcessClient.Start(serverCtx, serverReq)
	require.NoError(t, err)
	defer func() {
		serverCancel()
		if streamErr := serverStream.Close(); streamErr != nil {
			t.Logf("Error closing server stream: %v", streamErr)
		}
	}()

	// Wait for server to start.
	time.Sleep(time.Second)

	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	// Verify the server is accessible before pausing.
	client := &http.Client{Timeout: 5 * time.Second}
	resp := utils.WaitForStatus(t, client, sbx, proxyURL, port, nil, http.StatusOK)
	require.NotNil(t, resp)
	require.NoError(t, resp.Body.Close())

	// Pause the sandbox.
	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

	// Verify sandbox is paused.
	res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())
	require.Equal(t, api.Paused, res.JSON200.State)

	// Make a proxy request to trigger auto-resume.
	resumeClient := &http.Client{Timeout: 10 * time.Second}
	req := utils.NewRequest(sbx, proxyURL, port, nil)
	resp, err = resumeClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "expected server response after auto-resume")

	// Verify the sandbox is running — it must be since the server responded.
	res, err = c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())
	require.Equal(t, api.Running, res.JSON200.State, "sandbox should be running after auto-resume")
}

func TestSandboxNoAutoResumeWithoutFlag(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	ctx := t.Context()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
	envdClient := setup.GetEnvdClient(t, ctx)

	// Run ls before pausing.
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "ls")
	require.NoError(t, err)

	// Pause the sandbox.
	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

	// Verify sandbox is paused.
	res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())
	require.Equal(t, api.Paused, res.JSON200.State)

	// Attempt to exec — should fail since auto-resume is not enabled.
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "ls")
	require.Error(t, err)

	// Verify the sandbox is still paused.
	res, err = c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())
	require.Equal(t, api.Paused, res.JSON200.State, "sandbox should still be paused without auto-resume")
}
