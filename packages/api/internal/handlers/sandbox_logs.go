package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	apiedge "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxIDLogs(c *gin.Context, sandboxID string, params api.GetSandboxesSandboxIDLogsParams) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		telemetry.WithTeamID(team.ID.String()),
	)

	/// Sandboxes living in a cluster
	sbxLogs, err := a.getClusterSandboxLogs(ctx, sandboxID, team.ID.String(), utils.WithClusterFallback(team.ClusterID), params.Limit, params.Start)
	if err != nil {
		a.sendAPIStoreError(c, int(err.Code), err.Message)
		return
	}

	c.JSON(http.StatusOK, sbxLogs)
}

func (a *APIStore) getClusterSandboxLogs(ctx context.Context, sandboxID string, teamID string, clusterID uuid.UUID, qLimit *int32, qStart *int64) (*api.SandboxLogs, *api.Error) {
	cluster, ok := a.clustersPool.GetClusterById(clusterID)
	if !ok {
		telemetry.ReportCriticalError(ctx, "error getting cluster by ID", fmt.Errorf("cluster with ID '%s' not found", clusterID))
		return nil, &api.Error{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("Error getting cluster '%s'", clusterID),
		}
	}

	res, err := cluster.GetHttpClient().V1SandboxLogsWithResponse(
		ctx, sandboxID, &apiedge.V1SandboxLogsParams{
			TeamID: teamID,
			Start:  qStart,
			Limit:  qLimit,
		},
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", err)
		return nil, &api.Error{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("Error returning logs for sandbox '%s'", sandboxID),
		}
	}

	if res.JSON200 == nil {
		telemetry.ReportCriticalError(ctx, "error when returning logs for sandbox", fmt.Errorf("unexpected response for sandbox '%s': %s", sandboxID, string(res.Body)))
		return nil, &api.Error{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("Error returning logs for sandbox '%s'", sandboxID),
		}
	}

	l := make([]api.SandboxLog, 0)
	for _, row := range res.JSON200.Logs {
		l = append(l, api.SandboxLog{Line: row.Line, Timestamp: row.Timestamp})
	}

	le := make([]api.SandboxLogEntry, 0)
	for _, row := range res.JSON200.LogEntries {
		le = append(
			le, api.SandboxLogEntry{
				Timestamp: row.Timestamp,
				Level:     api.LogLevel(row.Level),
				Message:   row.Message,
				Fields:    row.Fields,
			},
		)
	}

	return &api.SandboxLogs{Logs: l, LogEntries: le}, nil
}
