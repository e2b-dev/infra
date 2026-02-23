package handlers

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (a *APIStore) PostAdminTeamsTeamIDBuildsCancel(c *gin.Context, teamID uuid.UUID) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "cancel admin-team-builds")
	defer span.End()

	logger.L().Info(ctx, "Admin cancelling all builds for team", logger.WithTeamID(teamID.String()))

	builds, err := a.sqlcDB.GetCancellableTemplateBuildsByTeam(ctx, teamID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get builds")

		return
	}

	logger.L().Info(ctx, "Found builds to cancel",
		logger.WithTeamID(teamID.String()),
		zap.Int("count", len(builds)),
	)

	cancelledCount := atomic.Int64{}
	failedCount := atomic.Int64{}

	wg := errgroup.Group{}
	wg.SetLimit(10)

	for _, b := range builds {
		wg.Go(func() error {
			buildID := b.BuildID
			templateID := b.TemplateID
			clusterID := clusters.WithClusterFallback(b.ClusterID)

			// Stop the build on the orchestrator node if it's running
			if b.ClusterNodeID != nil {
				deleteErr := a.templateManager.DeleteBuild(ctx, buildID, templateID, clusterID, *b.ClusterNodeID)
				if deleteErr != nil {
					logger.L().Error(ctx, "Failed to delete build on node",
						zap.String("buildID", buildID.String()),
						zap.String("templateID", templateID),
						logger.WithTeamID(teamID.String()),
						zap.Error(deleteErr))
					failedCount.Add(1)

					return nil
				}
			}

			err := a.templateManager.SetStatus(ctx, buildID, dbtypes.BuildStatusGroupFailed, &templatemanagergrpc.TemplateBuildStatusReason{
				Message: "cancelled by admin",
			})
			if err != nil {
				logger.L().Error(ctx, "Failed to set build status to failed",
					zap.String("buildID", buildID.String()),
					zap.String("templateID", templateID),
					logger.WithTeamID(teamID.String()),
					zap.Error(err))
				failedCount.Add(1)
			} else {
				logger.L().Debug(ctx, "Successfully cancelled build",
					zap.String("buildID", buildID.String()),
					zap.String("templateID", templateID),
					logger.WithTeamID(teamID.String()))
				cancelledCount.Add(1)
			}

			return nil
		})
	}

	err = wg.Wait()
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to cancel builds")

		return
	}

	logger.L().Info(ctx, "Completed cancelling team builds",
		logger.WithTeamID(teamID.String()),
		zap.Int64("cancelled", cancelledCount.Load()),
		zap.Int64("failed", failedCount.Load()),
	)

	result := api.AdminBuildCancelResult{
		CancelledCount: int(cancelledCount.Load()),
		FailedCount:    int(failedCount.Load()),
	}

	c.JSON(http.StatusOK, result)
}
