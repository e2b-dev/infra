package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) PatchTeamsTeamId(c *gin.Context, teamId api.TeamId) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "update team")

	teamInfo := auth.MustGetTeamInfo(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamInfo.Team.ID.String()))

	var body api.UpdateTeamRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	if body.Name == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Name is required")

		return
	}

	row, err := s.db.UpdateTeamName(ctx, queries.UpdateTeamNameParams{
		TeamID: teamInfo.Team.ID,
		Name:   body.Name,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to update team name", zap.Error(err), logger.WithTeamID(teamInfo.Team.ID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to update team name")

		return
	}

	c.JSON(http.StatusOK, api.UpdateTeamResponse{
		Id:   row.ID,
		Name: row.Name,
	})
}
