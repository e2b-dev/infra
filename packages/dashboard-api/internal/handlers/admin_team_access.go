package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *APIStore) PutAdminTeamsTeamIDBan(c *gin.Context, teamID api.TeamID) {
	s.updateTeamAccess(c, teamID, "banned", func(ctx context.Context) (int64, error) {
		return s.db.Dashboard.SetTeamBanned(ctx, dashboardqueries.SetTeamBannedParams{IsBanned: true, TeamID: teamID})
	})
}

func (s *APIStore) DeleteAdminTeamsTeamIDBan(c *gin.Context, teamID api.TeamID) {
	s.updateTeamAccess(c, teamID, "unbanned", func(ctx context.Context) (int64, error) {
		return s.db.Dashboard.SetTeamBanned(ctx, dashboardqueries.SetTeamBannedParams{IsBanned: false, TeamID: teamID})
	})
}

// updateTeamAccess applies a ban or block change and drops the team's cached
// auth state, which is where the API reads these flags from.
func (s *APIStore) updateTeamAccess(c *gin.Context, teamID uuid.UUID, change string, update func(context.Context) (int64, error)) {
	ctx := c.Request.Context()

	rows, err := update(ctx)
	if err != nil {
		logger.L().Error(ctx, "updating team access state", logger.WithTeamID(teamID.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to update team access state")

		return
	}
	if rows == 0 {
		s.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

		return
	}

	logger.L().Info(ctx, "admin team "+change, logger.WithTeamID(teamID.String()))

	if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
		logger.L().Error(ctx, "invalidating team cache after access state change",
			logger.WithTeamID(teamID.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to invalidate team cache after access state change")

		return
	}

	c.Status(http.StatusNoContent)
}
