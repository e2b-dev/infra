package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// buildDeleteTimeout bounds the node-side delete once the build has been
// recorded as failed.
const buildDeleteTimeout = 30 * time.Second

type buildCanceller interface {
	SetTerminalStatus(ctx context.Context, buildID uuid.UUID, statusGroup dbtypes.BuildStatusGroup, reason *templatemanagergrpc.TemplateBuildStatusReason) (bool, error)
	DeleteBuild(ctx context.Context, buildID uuid.UUID, templateID string, clusterID uuid.UUID, nodeID string) error
}

// cancelBuild ends a build and stops it on its node. The node-side delete takes
// the build's artifacts with it, so it only follows a write that ended the
// build: one that finished on its own between the listing and here keeps both
// its outcome and the artifacts that outcome refers to.
func cancelBuild(ctx context.Context, tm buildCanceller, build queries.GetCancellableTemplateBuildsByTeamRow) error {
	recorded, err := tm.SetTerminalStatus(ctx, build.BuildID, dbtypes.BuildStatusGroupFailed, &templatemanagergrpc.TemplateBuildStatusReason{
		Message: "cancelled by admin",
	})
	if err != nil {
		return fmt.Errorf("failed to set build status to failed: %w", err)
	}

	if !recorded || build.ClusterNodeID == nil {
		return nil
	}

	// The write above dropped the build from every listing, so nothing retries
	// this stop, and the request context dies with the caller or its deadline.
	deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), buildDeleteTimeout)
	defer cancel()

	err = tm.DeleteBuild(deleteCtx, build.BuildID, build.TemplateID, clusters.WithClusterFallback(build.ClusterID), *build.ClusterNodeID)
	if err != nil {
		return fmt.Errorf("failed to delete build on node: %w", err)
	}

	return nil
}

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

			err := cancelBuild(ctx, a.templateManager, b)
			if err != nil {
				logger.L().Error(ctx, "Failed to cancel build",
					zap.String("buildID", buildID.String()),
					zap.String("templateID", templateID),
					logger.WithTeamID(teamID.String()),
					zap.Error(err))
				failedCount.Add(1)

				return nil
			}

			logger.L().Debug(ctx, "Successfully cancelled build",
				zap.String("buildID", buildID.String()),
				zap.String("templateID", templateID),
				logger.WithTeamID(teamID.String()))
			cancelledCount.Add(1)

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
