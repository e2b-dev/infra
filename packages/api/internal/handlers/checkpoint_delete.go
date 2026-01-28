package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	typesteam "github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (a *APIStore) DeleteSandboxesSandboxIDCheckpointsCheckpointID(c *gin.Context, sandboxID api.SandboxID, checkpointID api.CheckpointID) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sandboxID = utils.ShortID(sandboxID)

	_, err := a.sqlcDB.GetCheckpoint(ctx, queries.GetCheckpointParams{
		CheckpointID: checkpointID,
		SandboxID:    sandboxID,
		TeamID:       teamID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Checkpoint '%s' not found for sandbox '%s'", checkpointID.String(), sandboxID))
			return
		}
		logger.L().Error(ctx, "Error getting checkpoint", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error deleting checkpoint")
		return
	}

	err = a.sqlcDB.DeleteCheckpoint(ctx, queries.DeleteCheckpointParams{
		CheckpointID: checkpointID,
		SandboxID:    sandboxID,
		TeamID:       teamID,
	})
	if err != nil {
		logger.L().Error(ctx, "Error deleting checkpoint", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error deleting checkpoint")
		return
	}

	c.Status(http.StatusNoContent)
}
