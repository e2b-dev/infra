package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetTeams(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list user teams")

	userID := auth.MustGetUserID(c)

	rows, err := s.db.Dashboard.GetDashboardTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		logger.L().Error(ctx, "failed to get user teams", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get user teams")

		return
	}

	teams := make([]api.UserTeam, 0, len(rows))
	for _, row := range rows {
		teams = append(teams, api.UserTeam{
			Id:                row.Team.ID,
			Name:              row.Team.Name,
			Slug:              row.Team.Slug,
			Tier:              row.Team.Tier,
			Email:             row.Team.Email,
			ProfilePictureUrl: row.Team.ProfilePictureUrl,
			IsBlocked:         row.Team.IsBlocked,
			IsBanned:          row.Team.IsBanned,
			BlockedReason:     row.Team.BlockedReason,
			IsDefault:         row.IsDefault,
			CreatedAt:         row.Team.CreatedAt,
			Limits: api.UserTeamLimits{
				MaxLengthHours:           row.TeamLimit.MaxLengthHours,
				ConcurrentSandboxes:      row.TeamLimit.ConcurrentSandboxes,
				ConcurrentTemplateBuilds: row.TeamLimit.ConcurrentTemplateBuilds,
				MaxVcpu:                  row.TeamLimit.MaxVcpu,
				MaxRamMb:                 row.TeamLimit.MaxRamMb,
				DiskMb:                   row.TeamLimit.DiskMb,
			},
		})
	}

	c.JSON(http.StatusOK, api.UserTeamsResponse{
		Teams: teams,
	})
}
