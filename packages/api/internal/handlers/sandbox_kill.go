package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) deleteSnapshot(ctx context.Context, sandboxID string, teamID uuid.UUID, teamClusterID *uuid.UUID) error {
	env, builds, err := a.db.GetSnapshotBuilds(ctx, sandboxID, teamID)
	if err != nil {
		return err
	}

	dbErr := a.db.DeleteEnv(ctx, env.ID)
	if dbErr != nil {
		return fmt.Errorf("error deleting env from db: %w", dbErr)
	}

	go func() {
		// remove any snapshots when the sandbox is not running
		deleteCtx, span := a.Tracer.Start(context.Background(), "delete-snapshot")
		defer span.End()
		span.SetAttributes(telemetry.WithSandboxID(sandboxID))
		span.SetAttributes(telemetry.WithTemplateID(env.ID))

		envBuildIDs := make([]template_manager.DeleteBuild, 0)
		for _, build := range builds {
			envBuildIDs = append(
				envBuildIDs,
				template_manager.DeleteBuild{
					BuildID:    build.ID,
					TemplateID: *build.EnvID,

					ClusterID:     teamClusterID,
					ClusterNodeID: build.ClusterNodeID,
				},
			)
		}

		if len(envBuildIDs) == 0 {
			return
		}

		deleteJobErr := a.templateManager.DeleteBuilds(deleteCtx, envBuildIDs)
		if deleteJobErr != nil {
			telemetry.ReportError(deleteCtx, "error deleting snapshot builds", deleteJobErr, telemetry.WithSandboxID(sandboxID))
		}
	}()

	a.templateCache.Invalidate(env.ID)

	return nil
}

func (a *APIStore) DeleteSandboxesSandboxID(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team
	teamID := team.ID

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		telemetry.WithTeamID(teamID.String()),
	)

	telemetry.ReportEvent(ctx, "killing sandbox")

	sbx, err := a.orchestrator.GetSandbox(sandboxID)
	if err == nil {
		if *sbx.TeamID != teamID {
			telemetry.ReportCriticalError(ctx, "sandbox does not belong to team", fmt.Errorf("sandbox '%s' does not belong to team '%s'", sandboxID, teamID.String()))

			a.sendAPIStoreError(c, http.StatusUnauthorized, fmt.Sprintf("Error deleting sandbox - sandbox '%s' does not belong to your team '%s'", sandboxID, teamID.String()))

			return
		}

		// remove running sandbox from the orchestrator
		sandboxExists := a.orchestrator.DeleteInstance(ctx, sandboxID, false)
		if !sandboxExists {
			telemetry.ReportError(ctx, "sandbox not found", fmt.Errorf("sandbox '%s' not found", sandboxID), telemetry.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error deleting sandbox - sandbox '%s' was not found", sandboxID))

			return
		}

		// remove any snapshots of the sandbox
		err := a.deleteSnapshot(ctx, sandboxID, teamID, team.ClusterID)
		if err != nil && !errors.Is(err, db.EnvNotFound{}) {
			telemetry.ReportError(ctx, "error deleting sandbox", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error deleting sandbox: %s", err))

			return
		}

		telemetry.ReportEvent(ctx, "deleted sandbox from orchestrator")

		c.Status(http.StatusNoContent)

		return
	}

	// remove any snapshots when the sandbox is not running
	deleteSnapshotErr := a.deleteSnapshot(ctx, sandboxID, teamID, team.ClusterID)
	if errors.Is(deleteSnapshotErr, db.EnvNotFound{}) {
		telemetry.ReportError(ctx, "snapshot for sandbox not found", fmt.Errorf("snapshot for sandbox '%s' not found", sandboxID), telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error deleting sandbox - sandbox '%s' not found", sandboxID))

		return
	}

	if deleteSnapshotErr != nil {
		telemetry.ReportError(ctx, "error deleting sandbox", deleteSnapshotErr)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error deleting sandbox: %s", deleteSnapshotErr))

		return
	}

	c.Status(http.StatusNoContent)
}
