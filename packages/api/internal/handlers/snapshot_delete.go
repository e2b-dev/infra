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
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (a *APIStore) DeleteSnapshotsSnapshotID(c *gin.Context, snapshotID api.SnapshotID) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(*typesteam.Team)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	_, err := a.sqlcDB.GetSnapshotTemplate(ctx, queries.GetSnapshotTemplateParams{
		SnapshotID: snapshotID,
		TeamID:     teamID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Snapshot '%s' not found", snapshotID))
			return
		}
		logger.L().Error(ctx, "Error getting snapshot", zap.Error(err), zap.String("snapshot_id", snapshotID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error deleting snapshot")
		return
	}

	err = a.sqlcDB.DeleteSnapshotTemplate(ctx, queries.DeleteSnapshotTemplateParams{
		SnapshotID: snapshotID,
		TeamID:     teamID,
	})
	if err != nil {
		logger.L().Error(ctx, "Error deleting snapshot", zap.Error(err), zap.String("snapshot_id", snapshotID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error deleting snapshot")
		return
	}

	c.Status(http.StatusNoContent)
}
