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

	removed := false

	sbx, err := a.orchestrator.GetSandbox(sandboxID, true)
	if err == nil {
		if sbx.Data().TeamID != teamID {
			telemetry.ReportCriticalError(ctx, "sandbox does not belong to team", fmt.Errorf("sandbox '%s' does not belong to team '%s'", sandboxID, teamID.String()))

			a.sendAPIStoreError(c, http.StatusUnauthorized, fmt.Sprintf("Error deleting sandbox - sandbox '%s' does not belong to your team '%s'", sandboxID, teamID.String()))

			return
		}

		err = a.killRunningSandbox(ctx, sbx)
		if err != nil {
			data := sbx.Data()
			switch data.State {
			case instance.StateFailed:
				zap.L().Info("Sandbox is in failed state", logger.WithSandboxID(sandboxID), zap.Error(data.Reason))
				a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error killing sandbox - sandbox '%s' is in failed state", sandboxID))
			default:
				telemetry.ReportError(ctx, "error deleting sandbox", err)
				a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error deleting sandbox: %s", err))
			}
			return
		}

		removed = true
	}

	// remove any snapshots when the sandbox is not running
	deleteSnapshotErr := a.deleteSnapshot(ctx, sandboxID, teamID, team.ClusterID)
	switch {
	case errors.Is(deleteSnapshotErr, db.EnvNotFoundError{}):
		zap.L().Debug("Snapshot for sandbox not found", logger.WithSandboxID(sandboxID))
	case deleteSnapshotErr != nil:
		telemetry.ReportError(ctx, "error deleting sandbox", deleteSnapshotErr)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error deleting sandbox: %s", deleteSnapshotErr))
		return
	default:
		removed = true
	}

	if removed {
		c.Status(http.StatusNoContent)
	} else {
		a.sendAPIStoreError(c, http.StatusNotFound, "Sandbox not found")
	}
}

func (a *APIStore) killRunningSandbox(ctx context.Context, sbx *instance.InstanceInfo) error {
	finish, err := sbx.StartChangingState(ctx, instance.StateKilled)
	if err != nil {
		return fmt.Errorf("error while trying to kill sandbox: %w", err)
	}
	if finish == nil {
		zap.L().Info("Sandbox was killed in another request", logger.WithSandboxID(sbx.SandboxID()))
		return nil
	}
	defer finish(err)

	err = a.orchestrator.RemoveInstance(ctx, sbx.SandboxID(), instance.RemoveTypeKill)
	if err != nil {
		return fmt.Errorf("error removing sandbox: %w", err)
	}

	return nil
}
