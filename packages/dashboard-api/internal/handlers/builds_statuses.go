package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardutils "github.com/e2b-dev/infra/packages/dashboard-api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	buildIdsLimit = int32(100)
)

func (s *APIStore) GetBuildsStatuses(c *gin.Context, params api.GetBuildsStatusesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "get build statuses")

	teamID := auth.MustGetTeamInfo(c).Team.ID
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	if len(params.BuildIds) > int(buildIdsLimit) {
		logger.L().Warn(ctx, "Too many build IDs", zap.Int("build_ids_count", len(params.BuildIds)), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusBadRequest, "Too many build IDs")

		return
	}

	p := queries.GetBuildsStatusesByTeamParams{
		TeamID:   teamID,
		BuildIds: params.BuildIds,
	}

	rows, err := s.db.GetBuildsStatusesByTeam(ctx, p)
	if err != nil {
		logger.L().Error(ctx, "Error getting build statuses", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting build statuses")

		return
	}

	buildStatuses := make([]api.BuildStatusItem, 0, len(rows))

	for _, record := range rows {
		buildStatuses = append(buildStatuses, api.BuildStatusItem{
			Id:            record.ID,
			Status:        dashboardutils.MapBuildStatusFromDBStatusGroup(record.StatusGroup),
			FinishedAt:    record.FinishedAt,
			StatusMessage: dashboardutils.MapBuildStatusMessageFromDBStatusGroup(record.StatusGroup, record.Reason),
		})
	}

	response := api.BuildsStatusesResponse{
		BuildStatuses: buildStatuses,
	}

	c.JSON(http.StatusOK, response)
}
