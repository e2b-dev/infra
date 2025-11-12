package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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
	c := setup.GetAPIClient()

	sbxNetAllowPublic := false
	sbxNet := &api.SandboxNetworkConfig{
		AllowPublicAccess: &sbxNetAllowPublic,
	}
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet))
	require.NotNil(t, sbx.TrafficAccessToken)

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	resp := waitForStatus(t, client, sbx, url, 8080, nil, http.StatusForbidden)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "Sandbox is secured with traffic access token. Access token header is missing\n", string(body))
}

func TestSandboxWithEnabledTrafficAccessTokenButInvalidHeader(t *testing.T) {
	c := setup.GetAPIClient()

	sbxNetAllowPublic := false
	sbxNet := &api.SandboxNetworkConfig{
		AllowPublicAccess: &sbxNetAllowPublic,
	}
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet))
	require.NotNil(t, sbx.TrafficAccessToken)
	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	headers := &http.Header{"x-e2b-traffic-access-token": []string{"abcd"}}
	resp := waitForStatus(t, client, sbx, url, 8080, headers, http.StatusForbidden)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "Sandbox is secured with traffic access token. Provided access token is invalid.\n", string(body))
}

func TestSandboxWithEnabledTrafficAccessToken(t *testing.T) {
	c := setup.GetAPIClient()

	sbxNetAllowPublic := false
	sbxNet := &api.SandboxNetworkConfig{
		AllowPublicAccess: &sbxNetAllowPublic,
	}
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet))
	require.NotNil(t, sbx.TrafficAccessToken)

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	port := 8080
	headers := &http.Header{"x-e2b-traffic-access-token": []string{*sbx.TrafficAccessToken}}
	resp := waitForStatus(t, client, sbx, url, port, headers, http.StatusBadGateway)
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
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "The sandbox is running but port is not open", errorResp.Message)
	assert.Equal(t, sbx.SandboxID, errorResp.SandboxID)
	assert.Equal(t, port, errorResp.Port)
}

func TestEnvdPortIsNotAffectedByTrafficAccessToken(t *testing.T) {
	c := setup.GetAPIClient()

	sbxNetAllowPublic := false
	sbxNet := &api.SandboxNetworkConfig{
		AllowPublicAccess: &sbxNetAllowPublic,
	}
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithNetwork(sbxNet))
	require.NotNil(t, sbx.TrafficAccessToken)

	url, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{
		Timeout: 1000 * time.Second,
	}

	resp := waitForStatus(t, client, sbx, url, int(consts.DefaultEnvdServerPort), nil, http.StatusNotFound)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
