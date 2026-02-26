package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardutils "github.com/e2b-dev/infra/packages/dashboard-api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetBuildsBuildId(c *gin.Context, buildId api.BuildId) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "get build details")
	teamID := auth.MustGetTeamInfo(c).Team.ID
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()), telemetry.WithBuildID(buildId.String()))

	row, err := s.db.GetBuildInfoByTeamAndBuildID(ctx, queries.GetBuildInfoByTeamAndBuildIDParams{
		TeamID:  teamID,
		BuildID: buildId,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Build not found or you don't have access to it")

			return
		}

		logger.L().Error(ctx, "Error getting build info", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithBuildID(buildId.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting build info")

		return
	}

	c.JSON(http.StatusOK, api.BuildInfo{
		Names:         &row.Names,
		CreatedAt:     row.CreatedAt,
		FinishedAt:    row.FinishedAt,
		Status:        dashboardutils.MapBuildStatusFromDBStatus(row.Status),
		StatusMessage: dashboardutils.MapBuildStatusMessageFromDBStatus(row.Status, row.Reason),
	})
}
