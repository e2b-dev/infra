package handlers

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (a *APIStore) PostAdminTeamsTeamIDSandboxesKill(c *gin.Context, teamID string) {
	ctx := c.Request.Context()
	ctx, span := tracer.Start(ctx, "admin-kill-team-sandboxes")
	defer span.End()

	// 1. Parse and validate team ID
	teamUUID, err := uuid.Parse(teamID)
	if err != nil {
		logger.L().Debug(ctx, "Invalid team ID", zap.String("teamID", teamID), zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid team ID: %s", err))

		return
	}

	logger.L().Info(ctx, "Admin killing all sandboxes for team", zap.String("teamID", teamID))

	// 2. Get all running/pausing sandboxes for team
	sandboxes := a.orchestrator.GetSandboxes(ctx, teamUUID, []sandbox.State{sandbox.StateRunning})
	logger.L().Info(ctx, "Found sandboxes to kill",
		zap.String("teamID", teamID),
		zap.Int("count", len(sandboxes)),
	)

	// 3. Kill each sandbox
	killedCount := atomic.Int64{}
	failedCount := atomic.Int64{}

	wg := sync.WaitGroup{}
	for _, sbx := range sandboxes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := a.orchestrator.RemoveSandbox(ctx, sbx, sandbox.StateActionKill)
			if err != nil {
				logger.L().Error(ctx, "Failed to kill sandbox",
					logger.WithSandboxID(sbx.SandboxID),
					logger.WithTeamID(teamID),
					zap.Error(err))
				failedCount.Add(1)
			} else {
				logger.L().Debug(ctx, "Successfully killed sandbox",
					logger.WithSandboxID(sbx.SandboxID),
					logger.WithTeamID(teamID))
				killedCount.Add(1)
			}
		}()
	}

	wg.Wait()
	logger.L().Info(ctx, "Completed killing team sandboxes",
		zap.String("teamID", teamID),
		zap.Int64("killed", killedCount.Load()),
		zap.Int64("failed", failedCount.Load()),
	)

	// 5. Return result
	result := api.BulkKillResult{
		KilledCount: int(killedCount.Load()),
		FailedCount: int(failedCount.Load()),
	}

	c.JSON(http.StatusOK, result)
}
