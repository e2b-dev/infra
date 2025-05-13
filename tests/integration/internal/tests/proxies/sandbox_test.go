package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/publicsuffix"

	"github.com/e2b-dev/infra/tests/integration/internal/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxProxyPorts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	envdClient := setup.GetEnvdClient(t, ctx)

	serverCtx, serverCancel := context.WithCancel(ctx)
	serverPort := 8000
	// Start Python HTTP server
	serverReq := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "python",
			Args: []string{"-m", "http.server", fmt.Sprintf("%d", serverPort)},
		},
	})
	setup.SetSandboxHeader(serverReq.Header(), sbx.SandboxID, sbx.ClientID)
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

	// Test good port
	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	etldPlusOne, _ := publicsuffix.EffectiveTLDPlusOne(url.Hostname())
	// Extract top level domain from EnvdProxy
	host := fmt.Sprintf("%d-%s-%s.%s", serverPort, sbx.SandboxID, sbx.ClientID, etldPlusOne)
	url.Host = host

	var goodResp *http.Response
	for i := 0; i < 10; i++ {
		req := &http.Request{
			Method: http.MethodGet,
			URL:    url,
			Header: http.Header{
				"Host": []string{host},
			},
		}
		goodResp, err = client.Do(req)
		if err == nil && goodResp.StatusCode == http.StatusOK {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NotNil(t, goodResp)
	assert.Equal(t, http.StatusOK, goodResp.StatusCode)

	// Test bad port (3210)
	badPort := 3210
	host = fmt.Sprintf("%d-%s-%s.%s", badPort, sbx.SandboxID, sbx.ClientID, etldPlusOne)
	url.Host = host

	var badResp *http.Response
	for i := 0; i < 10; i++ {
		req := &http.Request{
			Method: http.MethodGet,
			URL:    url,
			Header: http.Header{
				"Host": []string{host},
			},
		}
		badResp, err = client.Do(req)
		if err == nil && badResp.StatusCode == http.StatusBadGateway {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NotNil(t, badResp)
	assert.Equal(t, http.StatusBadGateway, badResp.StatusCode)

	// Parse error response
	var errorResp struct {
		Message   string `json:"message"`
		SandboxID string `json:"sandboxId"`
		Port      int    `json:"port"`
	}
	err = json.NewDecoder(badResp.Body).Decode(&errorResp)
	require.NoError(t, err)
	assert.Equal(t, "The sandbox is running but port is not open", errorResp.Message)
	assert.Equal(t, sbx.SandboxID, errorResp.SandboxID)
	assert.Equal(t, badPort, errorResp.Port)
}
