package handlers

import (
	"net/http"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardutils "github.com/e2b-dev/infra/packages/dashboard-api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func (s *APIStore) GetBuildsStatuses(c *gin.Context, params api.GetBuildsStatusesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "get build statuses")

	teamID := auth.MustGetTeamInfo(c).Team.ID
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	buildIDs := make([]uuid.UUID, len(params.BuildIds))
	for i, buildID := range params.BuildIds {
		buildIDs[i] = uuid.UUID(buildID)
	}

	p := queries.GetBuildsStatusesByTeamParams{
		TeamID:   teamID,
		BuildIds: buildIDs,
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
