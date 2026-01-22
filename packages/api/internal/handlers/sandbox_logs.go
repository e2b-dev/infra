package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxIDLogs(c *gin.Context, sandboxID string, params api.GetSandboxesSandboxIDLogsParams) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)
	team := c.Value(auth.TeamContextKey).(*types.Team)

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
		telemetry.WithSandboxID(sandboxID),
	)

	clusterID := utils.WithClusterFallback(team.ClusterID)
	cluster, ok := a.clusters.GetClusterById(clusterID)
	if !ok {
		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, fmt.Sprintf("Error getting cluster '%s'", clusterID), nil)

		return
	}

	logs, apiErr := cluster.GetResources().GetSandboxLogs(ctx, team.ID.String(), sandboxID, params.Start, params.Limit)
	if apiErr != nil {
		a.sendAPIStoreError(ctx, c, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	c.JSON(http.StatusOK, logs)
}
