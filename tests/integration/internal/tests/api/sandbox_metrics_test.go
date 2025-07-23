package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxMetrics(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox for testing
	sbx := utils.SetupSandboxWithCleanup(t, c)

	// Ensure there are some metrics
	time.Sleep(7 * time.Second)

	response, err := c.GetSandboxesSandboxIDMetricsWithResponse(t.Context(), sbx.SandboxID, &api.GetSandboxesSandboxIDMetricsParams{}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())

	require.NotNil(t, response.JSON200)
	require.Greater(t, len(*response.JSON200), 0, "Expected at least one metric in the response")
	for _, metric := range *response.JSON200 {
		require.NotEmpty(t, metric.CpuCount)
		require.NotEmpty(t, metric.CpuUsedPct)
		require.NotEmpty(t, metric.MemUsed)
		require.NotEmpty(t, metric.MemTotal)
		require.NotEmpty(t, metric.DiskUsed)
		require.NotEmpty(t, metric.DiskTotal)
		require.NotEmpty(t, metric.Timestamp)
	}
}
