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

	// Wait for sandbox to be paused.
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())

		if res.JSON200.State == api.Paused {
			break
		}

		require.True(t, time.Now().Before(deadline), "sandbox did not pause in time, state: %s", res.JSON200.State)
		time.Sleep(100 * time.Millisecond)
	}

	// Run ls again — this should trigger auto-resume.
	// The auto-resume is async, so retry until the sandbox is back up.
	deadline = time.Now().Add(30 * time.Second)
	for {
		err = utils.ExecCommand(t, ctx, sbx, envdClient, "ls")
		if err == nil {
			break
		}

		if time.Now().After(deadline) {
			require.NoError(t, err, "exec command did not succeed after auto-resume within timeout")
		}

		t.Logf("Exec failed (retrying): %v", err)
		time.Sleep(100 * time.Millisecond)
	}

	// Verify the sandbox is running again.
	res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
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

	// Wait for sandbox to be paused.
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())

		if res.JSON200.State == api.Paused {
			break
		}

		require.True(t, time.Now().Before(deadline), "sandbox did not pause in time, state: %s", res.JSON200.State)
		time.Sleep(100 * time.Millisecond)
	}

	// Make a proxy request to trigger auto-resume. The auto-resume is async,
	// so retry until the sandbox is back up and the server responds.
	resumeClient := &http.Client{Timeout: 10 * time.Second}
	deadline = time.Now().Add(60 * time.Second)
	for {
		req := utils.NewRequest(sbx, proxyURL, port, nil)
		resp, err = resumeClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}

		if resp != nil {
			resp.Body.Close()
		}

		if time.Now().After(deadline) {
			if err != nil {
				require.NoError(t, err, "proxy request did not succeed after auto-resume within timeout")
			}
			require.Equal(t, http.StatusOK, resp.StatusCode, "expected server response after auto-resume")
		}

		t.Logf("Proxy request failed (retrying): err=%v", err)
		time.Sleep(100 * time.Millisecond)
	}

	defer resp.Body.Close()

	// Verify the sandbox is running — it must be since the server responded.
	res, err := c.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.NotNil(t, res.JSON200, "expected 200 response, got status %d", res.StatusCode())
	require.Equal(t, api.Running, res.JSON200.State, "sandbox should be running after auto-resume")
}
