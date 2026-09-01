package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetTeamsTeamIDLimits(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "get team limits")

	if _, ok := auth.GetTeamInfo(c); ok {
		if _, ok := s.requireAuthedTeamMatchesPath(c, teamID); !ok {
			return
		}
	}

	row, err := s.authDB.GetTeamWithTierByTeamID(ctx, teamID)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

			return
		}

		logger.L().Error(ctx, "failed to get team limits", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get team limits")

		return
	}

	limits := row.TeamLimit
	c.JSON(http.StatusOK, api.TeamLimitsResponse{
		Tier: row.Team.Tier,
		Limits: api.UserTeamLimits{
			MaxLengthHours:           limits.MaxLengthHours,
			ConcurrentSandboxes:      limits.ConcurrentSandboxes,
			ConcurrentTemplateBuilds: limits.ConcurrentTemplateBuilds,
			MaxVcpu:                  limits.MaxVcpu,
			MaxRamMb:                 limits.MaxRamMb,
			DiskMb:                   limits.DiskMb,
			EventsTtlDays:            limits.EventsTtlDays,
		},
	})
}
