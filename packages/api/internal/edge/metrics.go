package edge

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	apiedge "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func GetClusterSandboxMetrics(ctx context.Context, pool *Pool, sandboxID string, teamID string, clusterID uuid.UUID, qStart *int64, qEnd *int64) ([]api.SandboxMetric, *api.APIError) {
	cluster, ok := pool.GetClusterById(clusterID)
	if !ok {
		return nil, &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: fmt.Sprintf("Error getting cluster '%s'", clusterID),
			Err:       fmt.Errorf("cluster with ID '%s' not found", clusterID),
		}
	}

	res, err := cluster.GetHttpClient().V1SandboxMetricsWithResponse(
		ctx, sandboxID, &apiedge.V1SandboxMetricsParams{
			TeamID: teamID,
			Start:  qStart,
			End:    qEnd,
		},
	)
	if err != nil {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("Error getting metrics for sandbox '%s'", sandboxID),
			Err:       fmt.Errorf("error getting metrics for sandbox '%s': %w", sandboxID, err),
		}
	}

	if res.StatusCode() != http.StatusOK {
		return nil, &api.APIError{
			Code:      res.StatusCode(),
			ClientMsg: fmt.Sprintf("Error getting metrics for sandbox '%s'", sandboxID),
			Err:       fmt.Errorf("unexpected response for sandbox - HTTP status '%d'", res.StatusCode()),
		}
	}

	if res.JSON200 == nil {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("Error getting metrics for sandbox '%s'", sandboxID),
			Err:       fmt.Errorf("no metrics returned for sandbox '%s'", sandboxID),
		}
	}

	// Transform edge types (snake_case) to API types (camelCase)

	apiMetrics := make([]api.SandboxMetric, len(*res.JSON200))
	for i, m := range *res.JSON200 {
		apiMetrics[i] = api.SandboxMetric{
			Timestamp:     m.Timestamp,
			TimestampUnix: m.TimestampUnix,
			CpuUsedPct:    m.CpuUsedPct,
			CpuCount:      m.CpuCount,
			DiskTotal:     m.DiskTotal,
			DiskUsed:      m.DiskUsed,
			MemTotal:      m.MemTotal,
			MemUsed:       m.MemUsed,
		}
	}

	return apiMetrics, nil
}

func GetClusterSandboxListMetrics(ctx context.Context, pool *Pool, teamID string, clusterID uuid.UUID, sandboxIDs []string) (map[string]api.SandboxMetric, *api.APIError) {
	cluster, ok := pool.GetClusterById(clusterID)
	if !ok {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("Error getting cluster '%s'", clusterID),
			Err:       fmt.Errorf("cluster with ID '%s' not found", clusterID),
		}
	}

	res, err := cluster.GetHttpClient().V1SandboxesMetricsWithResponse(
		ctx, &apiedge.V1SandboxesMetricsParams{
			TeamID:     teamID,
			SandboxIds: sandboxIDs,
		},
	)
	if err != nil {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Error getting metrics for sandbox list",
			Err:       fmt.Errorf("error getting metrics for sandbox list: %w", err),
		}
	}

	if res.StatusCode() != http.StatusOK {
		return nil, &api.APIError{
			Code:      res.StatusCode(),
			ClientMsg: "Error getting metrics for sandbox list",
			Err:       fmt.Errorf("unexpected response for sandbox list - HTTP status '%d'", res.StatusCode()),
		}
	}

	if res.JSON200 == nil {
		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Error getting metrics for sandbox list",
			Err:       fmt.Errorf("no metrics returned for sandbox list"),
		}
	}

	apiMetrics := make(map[string]api.SandboxMetric)
	for key, m := range res.JSON200.Sandboxes {
		apiMetrics[key] = api.SandboxMetric{
			Timestamp:     m.Timestamp,
			TimestampUnix: m.TimestampUnix,
			CpuUsedPct:    m.CpuUsedPct,
			CpuCount:      m.CpuCount,
			DiskTotal:     m.DiskTotal,
			DiskUsed:      m.DiskUsed,
			MemTotal:      m.MemTotal,
			MemUsed:       m.MemUsed,
		}
	}

	return apiMetrics, nil
}
