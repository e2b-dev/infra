package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxWithEnabledTrafficAccessTokenButMissingHeader(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	sbxNetAllowPublic := false
	sbxNet := &api.SandboxNetworkConfig{
		AllowPublicTraffic: &sbxNetAllowPublic,
	}
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet), utils.WithSecure(true))
	require.NotNil(t, sbx.TrafficAccessToken)
	require.NotNil(t, sbx.EnvdAccessToken)

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	sbx.TrafficAccessToken = nil // Simulate missing header

	port := 8080
	resp := utils.WaitForStatus(t, client, sbx, url, port, nil, http.StatusForbidden)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))

	// Parse error response
	var errorResp struct {
		Message   string `json:"message"`
		SandboxID string `json:"sandboxId"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResp)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "Sandbox is secured with traffic access token. Token header 'e2b-traffic-access-token' is missing", errorResp.Message)
	assert.Equal(t, sbx.SandboxID, errorResp.SandboxID)

	// Pretend to be a browser
	headers := &http.Header{"User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"}}
	resp = utils.WaitForStatus(t, client, sbx, url, port, headers, http.StatusForbidden)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.True(t, strings.HasPrefix(string(body), "<html"))
	assert.Contains(t, string(body), "Missing Traffic Access Token")
	assert.Contains(t, string(body), sbx.SandboxID)
	assert.True(t, strings.HasSuffix(string(body), "</html>"))
}

func TestSandboxWithEnabledTrafficAccessTokenButInvalidHeader(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	sbxNetAllowPublic := false
	sbxNet := &api.SandboxNetworkConfig{
		AllowPublicTraffic: &sbxNetAllowPublic,
	}
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet), utils.WithSecure(true))
	require.NotNil(t, sbx.TrafficAccessToken)
	require.NotNil(t, sbx.EnvdAccessToken)

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	// Simulate invalid header
	invalidTrafficAccessToken := "abcd"
	sbx.TrafficAccessToken = &invalidTrafficAccessToken

	port := 8080
	resp := utils.WaitForStatus(t, client, sbx, url, port, nil, http.StatusForbidden)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))

	// Parse error response
	var errorResp struct {
		Message   string `json:"message"`
		SandboxID string `json:"sandboxId"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResp)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "Sandbox is secured with traffic access token. Provided token in header 'e2b-traffic-access-token' is invalid", errorResp.Message)
	assert.Equal(t, sbx.SandboxID, errorResp.SandboxID)

	// Pretend to be a browser
	headers := &http.Header{"User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"}}
	resp = utils.WaitForStatus(t, client, sbx, url, port, headers, http.StatusForbidden)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.True(t, strings.HasPrefix(string(body), "<html"))
	assert.Contains(t, string(body), "Invalid Traffic Access Token")
	assert.Contains(t, string(body), sbx.SandboxID)
	assert.True(t, strings.HasSuffix(string(body), "</html>"))
}

func TestSandboxWithEnabledTrafficAccessToken(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	sbxNetAllowPublic := false
	sbxNet := &api.SandboxNetworkConfig{
		AllowPublicTraffic: &sbxNetAllowPublic,
	}
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet), utils.WithSecure(true))
	require.NotNil(t, sbx.TrafficAccessToken)
	require.NotNil(t, sbx.EnvdAccessToken)

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	port := 8080
	resp := utils.WaitForStatus(t, client, sbx, url, port, nil, http.StatusBadGateway)
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
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "The sandbox is running but port is not open", errorResp.Message)
	assert.Equal(t, sbx.SandboxID, errorResp.SandboxID)
	assert.Equal(t, port, errorResp.Port)
}

func TestEnvdPortIsNotAffectedByTrafficAccessToken(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	sbxNetAllowPublic := false
	sbxNet := &api.SandboxNetworkConfig{
		AllowPublicTraffic: &sbxNetAllowPublic,
	}
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet), utils.WithSecure(true))
	require.NotNil(t, sbx.TrafficAccessToken)
	require.NotNil(t, sbx.EnvdAccessToken)

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	headers := &http.Header{"X-Access-Token": []string{*sbx.EnvdAccessToken}}
	resp := utils.WaitForStatus(t, client, sbx, url, int(consts.DefaultEnvdServerPort), headers, http.StatusNotFound)
	require.NotNil(t, resp)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
