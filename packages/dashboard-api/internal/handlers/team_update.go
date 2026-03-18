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

func (s *APIStore) PatchTeamsTeamId(c *gin.Context, _ api.TeamId) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "update team")

	teamInfo := auth.MustGetTeamInfo(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamInfo.Team.ID.String()))

	var body api.UpdateTeamRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	if body.Name == nil && body.ProfilePictureUrl == nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "At least one field must be provided")

		return
	}

	if body.Name != nil && *body.Name == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Name must not be empty")

		return
	}

	row, err := s.db.UpdateTeam(ctx, queries.UpdateTeamParams{
		TeamID:            teamInfo.Team.ID,
		Name:              body.Name,
		ProfilePictureUrl: body.ProfilePictureUrl,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to update team", zap.Error(err), logger.WithTeamID(teamInfo.Team.ID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to update team")

		return
	}

	c.JSON(http.StatusOK, api.UpdateTeamResponse{
		Id:                row.ID,
		Name:              row.Name,
		ProfilePictureUrl: row.ProfilePictureUrl,
	})
}
