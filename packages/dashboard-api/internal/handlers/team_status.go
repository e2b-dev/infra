package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetTeamsTeamIDStatus(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "get team access status")

	var (
		team authqueries.Team
		err  error
	)

	if userID, ok := auth.GetUserID(c); ok {
		row, queryErr := s.authDB.GetTeamWithTierByTeamAndUser(ctx, authqueries.GetTeamWithTierByTeamAndUserParams{
			UserID: userID,
			ID:     teamID,
		})
		team, err = row.Team, queryErr
	} else {
		row, queryErr := s.authDB.GetTeamWithTierByTeamID(ctx, teamID)
		team, err = row.Team, queryErr
	}

	if err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

			return
		}

		logger.L().Error(ctx, "failed to get team access status", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get team access status")

		return
	}

	c.JSON(http.StatusOK, api.TeamStatusResponse{
		IsBlocked:     team.IsBlocked,
		IsBanned:      team.IsBanned,
		BlockedReason: team.BlockedReason,
	})
}
