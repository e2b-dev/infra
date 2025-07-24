package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxListMetrics(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox for testing
	sbx1 := utils.SetupSandboxWithCleanup(t, c)
	sbx2 := utils.SetupSandboxWithCleanup(t, c)

	// Ensure there are some metrics
	time.Sleep(7 * time.Second)

	response, err := c.GetSandboxesMetricsWithResponse(t.Context(), &api.GetSandboxesMetricsParams{SandboxIds: []string{sbx1.SandboxID, sbx2.SandboxID}}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())

	require.NotNil(t, response.JSON200)
	require.NotNil(t, response.JSON200.Sandboxes)
	require.Equal(t, 2, len(response.JSON200.Sandboxes), "Expected two metrics in the response")
	assert.Contains(t, response.JSON200.Sandboxes, sbx1.SandboxID, "Expected sandbox metrics to include the created sandbox")
	assert.Contains(t, response.JSON200.Sandboxes, sbx2.SandboxID, "Expected sandbox metrics to include the second created sandbox")
	for _, sbx := range response.JSON200.Sandboxes {
		assert.NotEmpty(t, sbx.Timestamp, "Metric timestamp should not be empty")
		assert.NotEmpty(t, sbx.CpuUsedPct, "Cpu pct should not be empty")
		assert.NotEmpty(t, sbx.CpuCount, "Cpu count should not be empty")
		assert.NotEmpty(t, sbx.MemUsed, "Memory used should not be empty")
		assert.NotEmpty(t, sbx.MemTotal, "Memory total should not be empty")
		assert.NotEmpty(t, sbx.DiskUsed, "Disk used should not be empty")
		assert.NotEmpty(t, sbx.DiskTotal, "Disk total should not be empty")
	}
}
