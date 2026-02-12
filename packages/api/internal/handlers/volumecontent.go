package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	clustershared "github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) executeOnOrchestrator(
	c *gin.Context,
	fn func(context.Context, *clusters.GRPCClient) error,
) {
	ctx := c.Request.Context()

	team, apiErr := a.GetTeam(ctx, c, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return
	}

	clusterID := clustershared.WithClusterFallback(team.ClusterID)

	if err := a.executeOnOrchestratorByClusterID(ctx, clusterID, fn); err != nil {
		if errors.Is(err, ErrClusterNotFound) {
			a.sendAPIStoreError(c, http.StatusNotFound, "cluster not found")
			telemetry.ReportError(ctx, "cluster not found", err)

			return
		}

		if code, ok := status.FromError(err); ok {
			switch code.Code() {
			case codes.NotFound:
				a.sendAPIStoreError(c, 404, "path not found")
				telemetry.ReportError(ctx, "path not found", err)

				return
			case codes.InvalidArgument:
				a.sendAPIStoreError(c, 400, "invalid argument")
				telemetry.ReportError(ctx, "invalid argument", err)

				return
			}
		}

		a.sendAPIStoreError(c, 500, "failed to execute on orchestrator")
		telemetry.ReportCriticalError(ctx, "error when executing on orchestrator", err)

		return
	}
}
