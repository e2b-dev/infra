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

func (s *APIStore) GetAgents(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list agents")
	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	rows, err := s.db.Dashboard.ListAgents(ctx, teamID)
	if err != nil {
		logger.L().Error(ctx, "failed to list agents", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to list agents")

		return
	}

	agents := make([]api.Agent, 0, len(rows))
	for _, row := range rows {
		agents = append(agents, api.Agent{
			Id:          row.ID,
			TeamId:      row.TeamID,
			Name:        row.Name,
			Template:    row.TemplateID,
			Description: row.Description,
			Command:     row.Command,
			Author:      row.Author,
			Public:      row.Public,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
			DeletedAt:   row.DeletedAt,
		})
	}

	c.JSON(http.StatusOK, api.AgentsResponse{
		Agents: agents,
	})
}
