package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	template_manager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

	go func(ctx context.Context) {
		// remove any snapshots when the sandbox is not running
		ctx, span := tracer.Start(ctx, "delete-snapshot")
		defer span.End()
		span.SetAttributes(telemetry.WithSandboxID(sandboxID))
		span.SetAttributes(telemetry.WithTemplateID(env.ID))

		envBuildIDs := make([]template_manager.DeleteBuild, 0)
		for _, build := range builds {
			envBuildIDs = append(
				envBuildIDs,
				template_manager.DeleteBuild{
					BuildID:    build.ID,
					TemplateID: build.EnvID,
					ClusterID:  utils.WithClusterFallback(teamClusterID),
					NodeID:     build.ClusterNodeID,
				},
			)
		}

		if len(envBuildIDs) == 0 {
			return
		}

		deleteJobErr := a.templateManager.DeleteBuilds(ctx, envBuildIDs)
		if deleteJobErr != nil {
			telemetry.ReportError(ctx, "error deleting snapshot builds", deleteJobErr, telemetry.WithSandboxID(sandboxID))
		}
	}(context.WithoutCancel(ctx))

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

	killedOrRemoved := false

	sbx, err := a.orchestrator.GetSandboxData(sandboxID, true)
	if err == nil {
		if sbx.TeamID != teamID {
			a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox \"%s\"", sandboxID))
			return
		}

		err = a.orchestrator.RemoveSandbox(ctx, sbx, instance.StateActionKill)
		switch {
		case err == nil:
			killedOrRemoved = true
		case errors.Is(err, orchestrator.ErrSandboxNotFound):
			zap.L().Debug("Sandbox not found", logger.WithSandboxID(sandboxID))
		case errors.Is(err, orchestrator.ErrSandboxOperationFailed):
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error killing sandbox: %s", err))
			return
		default:
			telemetry.ReportError(ctx, "error killing sandbox", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error killing sandbox: %s", err))
			return
		}
	} else {
		zap.L().Debug("Sandbox not found", logger.WithSandboxID(sandboxID))
	}

	// remove any snapshots when the sandbox is not running
	deleteSnapshotErr := a.deleteSnapshot(ctx, sandboxID, teamID, team.ClusterID)
	switch {
	case errors.Is(deleteSnapshotErr, db.EnvNotFoundError{}):
	case deleteSnapshotErr != nil:
		telemetry.ReportError(ctx, "error deleting sandbox", deleteSnapshotErr)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error deleting sandbox: %s", deleteSnapshotErr))
		return
	default:
		killedOrRemoved = true
	}

	if killedOrRemoved {
		c.Status(http.StatusNoContent)
	} else {
		a.sendAPIStoreError(c, http.StatusNotFound, "Sandbox not found")
	}
}
