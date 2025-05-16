package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// GetTemplates serves to list templates (e.g. in CLI)
func (a *APIStore) GetTemplates(c *gin.Context, params api.GetTemplatesParams) {
	ctx := c.Request.Context()

	userID := c.Value(auth.UserIDContextKey).(uuid.UUID)

	var team *models.Team
	teams, err := a.db.GetTeams(ctx, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting teams"))

		err = fmt.Errorf("error when getting teams: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	if params.TeamID != nil {
		teamUUID, err := uuid.Parse(*params.TeamID)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid team ID")

			telemetry.ReportError(ctx, err)

			return
		}

		for _, t := range teams {
			if t.ID == teamUUID {
				team = t
				break
			}
		}

		if team == nil {
			a.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

			telemetry.ReportError(ctx, fmt.Errorf("team not found"))

			return
		}
	} else {
		for _, t := range teams {
			if t.Edges.UsersTeams[0].IsDefault {
				team = t
				break
			}
		}

		if team == nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Default team not found")

			telemetry.ReportError(ctx, fmt.Errorf("default team not found"))

			return
		}
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		attribute.String("team.id", team.ID.String()),
	)

	envs, err := a.db.GetEnvs(ctx, team.ID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting sandbox templates")

		err = fmt.Errorf("error when getting envs: %w", err)
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	telemetry.ReportEvent(ctx, "listed environments")

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "listed environments", properties)

	templates := make([]*api.Template, 0, len(envs))
	for _, item := range envs {
		var createdBy *api.TeamUser
		if item.CreatedBy != nil {
			createdBy = &api.TeamUser{
				Id:    item.CreatedBy.Id,
				Email: item.CreatedBy.Email,
			}
		}

		templates = append(templates, &api.Template{
			TemplateID:    item.TemplateID,
			BuildID:       item.BuildID,
			CpuCount:      int32(item.VCPU),
			MemoryMB:      int32(item.RAMMB),
			Public:        item.Public,
			Aliases:       item.Aliases,
			CreatedAt:     item.CreatedAt,
			UpdatedAt:     item.UpdatedAt,
			LastSpawnedAt: item.LastSpawnedAt,
			SpawnCount:    item.SpawnCount,
			BuildCount:    item.BuildCount,
			CreatedBy:     createdBy,
		})
	}

	c.JSON(http.StatusOK, templates)
}
