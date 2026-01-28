package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

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

func (a *APIStore) GetSandboxesSandboxIDCheckpoints(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sandboxID = utils.ShortID(sandboxID)

	checkpoints, err := a.sqlcDB.ListCheckpoints(ctx, queries.ListCheckpointsParams{
		SandboxID: sandboxID,
		TeamID:    teamID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusOK, []api.CheckpointInfo{})
			return
		}
		logger.L().Error(ctx, "Error listing checkpoints", zap.Error(err), logger.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error listing checkpoints")
		return
	}

	result := make([]api.CheckpointInfo, 0, len(checkpoints))
	for _, cp := range checkpoints {
		name := extractCheckpointName(cp.Name)
		createdAt := cp.CreatedAt.Time

		result = append(result, api.CheckpointInfo{
			CheckpointID: cp.CheckpointID,
			SandboxID:    sandboxID,
			Name:         &name,
			CreatedAt:    createdAt,
		})
	}

	c.JSON(http.StatusOK, result)
}

func extractCheckpointName(tag string) string {
	if !strings.HasPrefix(tag, checkpointTagPrefix) {
		return tag
	}
	name := strings.TrimPrefix(tag, checkpointTagPrefix)
	parts := strings.Split(name, "_")
	if len(parts) > 1 {
		return strings.Join(parts[:len(parts)-1], "_")
	}
	return name
}
