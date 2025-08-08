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
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

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

	waitForOK := func() bool {
		resp, err := utils.DoRequest(t, client, sbx, url, port, nil)
		if err != nil {
			t.Logf("Error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return true
		}

		x, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("[Status code: %d] Error reading response body: %v", resp.StatusCode, err)
		} else {
			t.Logf("[Status code: %d] Response body: %s", resp.StatusCode, string(x))
		}

		return false
	}

	var resp *http.Response
	for i := 0; i < 10; i++ {
		if waitForOK() {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NotNil(t, resp)
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
	for i := 0; i < 10; i++ {
		resp, err = utils.DoRequest(t, client, sbx, url, port, nil)
		if err == nil && resp.StatusCode == http.StatusBadGateway {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
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
	for i := 0; i < 10; i++ {
		resp, err = utils.DoRequest(t, client, sbx, url, port, &http.Header{"User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"}})
		if err == nil && resp.StatusCode == http.StatusBadGateway {
			break
		} else if err != nil {
			x, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Logf("[Status code: %d] Error reading response body: %v", resp.StatusCode, err)
			} else {
				t.Logf("[Status code: %d] Response body: %s", resp.StatusCode, string(x))
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(string(body), "<html"))
	assert.True(t, strings.Contains(string(body), "no service running on port"))
	assert.True(t, strings.Contains(string(body), sbx.SandboxID))
	assert.True(t, strings.Contains(string(body), fmt.Sprintf("%d", port)))
	assert.True(t, strings.HasSuffix(string(body), "</html>"))
}
