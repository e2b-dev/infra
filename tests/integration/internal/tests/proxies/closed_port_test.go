package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func waitForStatus(t *testing.T, client *http.Client, sbx *api.Sandbox, url *url.URL, port int, headers *http.Header, expectedStatus int) (*http.Response, bool) {
	req := utils.NewRequest(sbx, url, port, headers)
	resp, err := client.Do(req)
	if err != nil {
		t.Logf("Error: %v", err)
		return nil, false
	}

	if resp.StatusCode == expectedStatus {
		return resp, true // todo: this prevents the `resp.Body.Close` from functioning correctly.
	}

	x, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Logf("[Status code: %d] Error reading response body: %v", resp.StatusCode, err)
	} else {
		t.Logf("[Status code: %d] Response body: %s", resp.StatusCode, string(x))
	}

	return nil, false
}

func TestSandboxProxyWorkingPort(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	envdClient := setup.GetEnvdClient(t, ctx)

	serverCtx, serverCancel := context.WithCancel(ctx)
	port := 8000
	// Start Python HTTP server
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
		serverErr := serverStream.Close()
		if serverErr != nil {
			t.Logf("Error closing server stream: %v", serverErr)
		}
	}()

	// Wait for server to start
	time.Sleep(time.Second)

	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	var resp *http.Response
	var ok bool
	for i := 0; i < 10; i++ {
		if resp, ok = waitForStatus(t, client, sbx, url, port, nil, http.StatusOK); ok {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSandboxProxyClosedPort(t *testing.T) {
	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	// Test closed port
	port := 3210

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	var resp *http.Response
	var ok bool
	for i := 0; i < 10; i++ {
		if resp, ok = waitForStatus(t, client, sbx, url, port, nil, http.StatusBadGateway); ok {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)

	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
	// Parse error response
	var errorResp struct {
		Message   string `json:"message"`
		SandboxID string `json:"sandboxId"`
		Port      int    `json:"port"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResp)
	require.NoError(t, err)
	assert.Equal(t, "The sandbox is running but port is not open", errorResp.Message)
	assert.Equal(t, sbx.SandboxID, errorResp.SandboxID)
	assert.Equal(t, port, errorResp.Port)

	// Pretend to be a browser
	headers := &http.Header{"User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"}}
	for i := 0; i < 10; i++ {
		if resp, ok = waitForStatus(t, client, sbx, url, port, headers, http.StatusBadGateway); ok {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.True(t, strings.HasPrefix(string(body), "<html"))
	assert.True(t, strings.Contains(string(body), "no service running on port"))
	assert.True(t, strings.Contains(string(body), sbx.SandboxID))
	assert.True(t, strings.Contains(string(body), fmt.Sprintf("%d", port)))
	assert.True(t, strings.HasSuffix(string(body), "</html>"))
}
