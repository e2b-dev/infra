package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetAgents(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list agents")

	rows, err := s.db.Dashboard.ListAgents(ctx)
	if err != nil {
		logger.L().Error(ctx, "failed to get agents", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get agents")

		return
	}

	agents := make([]api.Agent, 0, len(rows))
	for _, row := range rows {
		agents = append(agents, mapAgent(row))
	}

	c.JSON(http.StatusOK, api.AgentsResponse{
		Agents: agents,
	})
}

func mapAgent(row dashboardqueries.ListAgentsRow) api.Agent {
	name := row.Command
	if row.AgentName != nil && *row.AgentName != "" {
		name = *row.AgentName
	}

	description := row.Command + " coding agent."
	if row.AgentDescription != nil && *row.AgentDescription != "" {
		description = *row.AgentDescription
	}

	icon := "open"
	if row.AgentIcon != nil && *row.AgentIcon != "" {
		icon = *row.AgentIcon
	}

	return api.Agent{
		Id:          row.ID,
		Name:        name,
		Command:     row.Command,
		Template:    row.Template,
		Icon:        icon,
		Description: description,
	}
}
