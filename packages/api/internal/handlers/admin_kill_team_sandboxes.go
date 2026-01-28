package handlers

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (a *APIStore) PostAdminTeamsTeamIDSandboxesKill(c *gin.Context, teamID uuid.UUID) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "admin-kill-team-sandboxes")
	defer span.End()

	logger.L().Info(ctx, "Admin killing all sandboxes for team", logger.WithTeamID(teamID.String()))

	// Get all running sandboxes for the team
	sandboxes, err := a.orchestrator.GetSandboxes(ctx, teamID, []sandbox.State{sandbox.StateRunning})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get sandboxes")

		return
	}

	logger.L().Info(ctx, "Found sandboxes to kill",
		logger.WithTeamID(teamID.String()),
		zap.Int("count", len(sandboxes)),
	)

	killedCount := atomic.Int64{}
	failedCount := atomic.Int64{}

	wg := errgroup.Group{}
	wg.SetLimit(10)

	// Kill each sandbox
	for _, sbx := range sandboxes {
		wg.Go(func() error {
			err := a.orchestrator.RemoveSandbox(ctx, sbx, sandbox.StateActionKill)
			if err != nil {
				logger.L().Error(ctx, "Failed to kill sandbox",
					logger.WithSandboxID(sbx.SandboxID),
					logger.WithTeamID(teamID.String()),
					zap.Error(err))
				failedCount.Add(1)
			} else {
				logger.L().Debug(ctx, "Successfully killed sandbox",
					logger.WithSandboxID(sbx.SandboxID),
					logger.WithTeamID(teamID.String()))
				killedCount.Add(1)
			}

			return nil
		})
	}

	err = wg.Wait()
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to kill sandboxes")

		return
	}

	logger.L().Info(ctx, "Completed killing team sandboxes",
		zap.String("teamID", teamID.String()),
		zap.Int64("killed", killedCount.Load()),
		zap.Int64("failed", failedCount.Load()),
	)

	// Return result
	result := api.AdminSandboxKillResult{
		KilledCount: int(killedCount.Load()),
		FailedCount: int(failedCount.Load()),
	}

	c.JSON(http.StatusOK, result)
}
