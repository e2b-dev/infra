package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) deleteSnapshot(
	ctx context.Context,
	sandboxID string,
	teamID uuid.UUID,
) (*bool, error) {
	env, builds, err := a.db.GetSnapshotBuilds(ctx, sandboxID, teamID)
	if err != nil {
		return nil, fmt.Errorf("error when getting paused sandbox from db: %w", err)
	}

	var found bool = true
	if env == nil {
		found = false
		return &found, nil
	}

	dbErr := a.db.DeleteEnv(ctx, env.ID)
	if dbErr != nil {
		return nil, fmt.Errorf("error when deleting env from db: %w", dbErr)
	}

	a.templateCache.Invalidate(env.ID)

	buildIds := make([]uuid.UUID, len(builds))
	for i, build := range builds {
		buildIds[i] = build.ID
	}

	// delete all builds
	deleteJobErr := a.templateManager.DeleteBuilds(ctx, buildIds)
	if deleteJobErr != nil {
		return nil, fmt.Errorf("error when deleting builds from storage: %w", deleteJobErr)
	}

	return &found, nil
}

func (a *APIStore) DeleteSandboxesSandboxID(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	teamID := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team.ID

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		attribute.String("team.id", teamID.String()),
	)

	telemetry.ReportEvent(ctx, "killing sandbox")

	sbx, err := a.orchestrator.GetSandbox(sandboxID)
	if err == nil {
		if *sbx.TeamID != teamID {
			errMsg := fmt.Errorf("sandbox '%s' does not belong to team '%s'", sandboxID, teamID.String())
			telemetry.ReportCriticalError(ctx, errMsg)

			a.sendAPIStoreError(c, http.StatusUnauthorized, fmt.Sprintf("Error killing sandbox - sandbox '%s' does not belong to your team '%s'", sandboxID, teamID.String()))

			return
		}

		a.orchestrator.DeleteInstance(ctx, sandboxID)

		notFound, err := a.deleteSnapshot(ctx, sandboxID, teamID)
		if notFound != nil {
			telemetry.ReportEvent(ctx, "deleted snapshot")
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error deleting snapshot - %s not found", sandboxID))

			return
		}

		if err != nil {
			telemetry.ReportError(ctx, err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error deleting snapshot: %s", err))

			return
		}

		telemetry.ReportEvent(ctx, "deleted sandbox from orchestrator")

		c.Status(http.StatusNoContent)

		return
	}

	notFound, deleteSnapshotErr := a.deleteSnapshot(ctx, sandboxID, teamID)
	if notFound != nil {
		telemetry.ReportEvent(ctx, "deleted snapshot")
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error deleting snapshot - %s not found", sandboxID))

		return
	}

	if deleteSnapshotErr != nil {
		telemetry.ReportError(ctx, deleteSnapshotErr)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error deleting snapshot: %s", deleteSnapshotErr))

		return
	}

	c.Status(http.StatusNoContent)
}
