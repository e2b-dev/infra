package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func (s *APIStore) GetBuildsStatuses(c *gin.Context, params api.GetBuildsStatusesParams) {
	ctx := c.Request.Context()

	teamID := auth.MustGetTeamInfo(c).Team.ID

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
		logger.L().Error(ctx, "Error getting build statuses", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting build statuses")

		return
	}

	buildStatuses := make([]api.BuildStatusItem, 0, len(rows))

	for _, record := range rows {
		buildStatuses = append(buildStatuses, api.BuildStatusItem{
			Id:            record.ID,
			Status:        api.BuildStatus(record.StatusGroup),
			FinishedAt:    record.FinishedAt,
			StatusMessage: mapBuildStatusMessage(record.StatusGroup, record.Reason),
		})
	}

	response := api.BuildsStatusesResponse{
		BuildStatuses: buildStatuses,
	}

	c.JSON(http.StatusOK, response)
}

// UTILS

func mapBuildStatusMessage(status types.BuildStatusGroup, reason []byte) *string {

	if status != types.BuildStatusGroupFailed {
		return nil
	}

	if len(reason) == 0 {
		return nil
	}

	var data map[string]interface{}

	err := json.Unmarshal(reason, &data)
	if err != nil {
		return nil
	}

	message, ok := data["message"]
	if !ok {
		return nil
	}

	messageString, ok := message.(string)
	if !ok {
		return nil
	}

	return &messageString
}
