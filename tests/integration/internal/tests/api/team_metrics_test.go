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

func TestTeamMetrics(t *testing.T) {
	c := setup.GetAPIClient()

	// Create multiple sandboxes to generate team metrics
	utils.SetupSandboxWithCleanup(t, c)
	utils.SetupSandboxWithCleanup(t, c)

	// Wait a bit to ensure metrics are generated
	maxRetries := 15
	var metrics []api.TeamMetric
	for i := 0; i < maxRetries; i++ {
		response, err := c.GetTeamsTeamIDMetricsWithResponse(t.Context(), setup.TeamID, nil, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode())

		require.NotNil(t, response.JSON200)
		if len(*response.JSON200) == 0 {
			t.Logf("No team metrics found yet, retrying (%d/%d)", i+1, maxRetries)

			time.Sleep(1 * time.Second) // Wait before retrying
			continue
		}

		metrics = *response.JSON200
		break
	}

	// Test getting team metrics
	require.Greater(t, len(metrics), 0, "Expected at least one team metric in the response")

	// Verify the structure of team metrics
	startRateGreaterThanZero := false
	concurrentSandboxesGreaterThanZero := false

	for _, metric := range metrics {
		require.NotEmpty(t, metric.Timestamp, "Timestamp should not be empty")
		if metric.SandboxStartRate > 0 {
			startRateGreaterThanZero = true
		}
		if metric.ConcurrentSandboxes > 0 {
			concurrentSandboxesGreaterThanZero = true
		}
	}

	require.True(t, concurrentSandboxesGreaterThanZero, "MaxConcurrentSandboxes should be >= 0")
	require.True(t, startRateGreaterThanZero, "StartedSandboxes should be >= 0")
}

func TestTeamMetricsWithTimeRange(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox to generate metrics
	utils.SetupSandboxWithCleanup(t, c)

	// Test with custom time range (last hour)
	now := time.Now()
	start := now.Add(-1 * time.Hour).Unix()
	end := now.Unix()

	maxRetries := 15
	var metrics []api.TeamMetric
	for i := 0; i < maxRetries; i++ {
		response, err := c.GetTeamsTeamIDMetricsWithResponse(t.Context(), setup.TeamID, &api.GetTeamsTeamIDMetricsParams{
			Start: &start,
			End:   &end,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode())

		require.NotNil(t, response.JSON200)
		if len(*response.JSON200) == 0 {
			t.Logf("No team metrics found yet, retrying (%d/%d)", i+1, maxRetries)

			time.Sleep(1 * time.Second) // Wait before retrying
			continue
		}

		metrics = *response.JSON200
		break
	}

	require.Greater(t, len(metrics), 0, "Expected at least one team metric in the response")

	// Verify all timestamps are within the requested range
	for _, metric := range metrics {
		require.GreaterOrEqual(t, metric.Timestamp.Unix(), start, "Metric timestamp should be >= start time")
		require.LessOrEqual(t, metric.Timestamp.Unix(), end, "Metric timestamp should be <= end time")
	}
}

func TestTeamMetricsEmpty(t *testing.T) {
	c := setup.GetAPIClient()

	// Test getting metrics for a time range where no sandboxes existed
	now := time.Now()
	start := now.Add(-240 * time.Hour).Unix()
	end := now.Add(-216 * time.Hour).Unix()

	// Wait a bit to ensure metrics are generated
	response, err := c.GetTeamsTeamIDMetricsWithResponse(t.Context(), setup.TeamID, &api.GetTeamsTeamIDMetricsParams{
		Start: &start,
		End:   &end,
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)
	metrics := *response.JSON200
	require.Empty(t, metrics, "Expected no team metrics for historical time range")
}
