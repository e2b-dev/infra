package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) DeleteSnapshotsSnapshotID(c *gin.Context, snapshotID api.SnapshotID) {
	ctx := c.Request.Context()

	teamInfo := a.GetTeamInfo(c)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))
	telemetry.ReportEvent(ctx, "Deleting snapshot")

	_, err := a.sqlcDB.GetSnapshotTemplate(ctx, queries.GetSnapshotTemplateParams{
		SnapshotID: snapshotID,
		TeamID:     teamID,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Snapshot '%s' not found", snapshotID))

			return
		}
		logger.L().Error(ctx, "Error getting snapshot", zap.Error(err), zap.String("snapshot_id", snapshotID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error deleting snapshot")

		return
	}

	builds, err := a.sqlcDB.GetExclusiveBuildsForTemplateDeletion(ctx, snapshotID)
	if err != nil {
		telemetry.ReportError(ctx, "failed to get snapshot builds for deletion", err)
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

	go a.deleteSnapshotBuilds(context.WithoutCancel(ctx), snapshotID, builds, teamInfo.Team.ClusterID)

	a.templateCache.InvalidateAllTags(snapshotID)

	c.Status(http.StatusNoContent)
}

func (a *APIStore) deleteSnapshotBuilds(ctx context.Context, snapshotID string, builds []queries.GetExclusiveBuildsForTemplateDeletionRow, teamClusterID *uuid.UUID) {
	ctx, span := tracer.Start(ctx, "delete-snapshot-builds")
	defer span.End()
	span.SetAttributes(telemetry.WithTemplateID(snapshotID))

	buildIDs := make([]template_manager.DeleteBuild, 0, len(builds))
	for _, build := range builds {
		if build.ClusterNodeID == nil {
			continue
		}

		buildIDs = append(buildIDs, template_manager.DeleteBuild{
			BuildID:    build.BuildID,
			TemplateID: snapshotID,
			ClusterID:  utils.WithClusterFallback(teamClusterID),
			NodeID:     *build.ClusterNodeID,
		})
	}

	if len(buildIDs) == 0 {
		return
	}

	err := a.templateManager.DeleteBuilds(ctx, buildIDs)
	if err != nil {
		telemetry.ReportError(ctx, "error deleting snapshot builds from storage", err)
	} else {
		telemetry.ReportEvent(ctx, "deleted snapshot builds from storage")
	}
}
