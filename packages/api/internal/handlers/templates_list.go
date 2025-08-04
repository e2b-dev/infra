package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// GetTemplates serves to list templates (e.g. in CLI)
func (a *APIStore) GetTemplates(c *gin.Context, params api.GetTemplatesParams) {
	ctx := c.Request.Context()

	userID := c.Value(auth.UserIDContextKey).(uuid.UUID)

	var team *queries.Team
	teams, err := a.sqlcDB.GetTeamsWithUsersTeams(ctx, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting teams")

		telemetry.ReportCriticalError(ctx, "error when getting teams", err)

		return
	}

	if params.TeamID != nil {
		teamUUID, err := uuid.Parse(*params.TeamID)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid team ID")

			telemetry.ReportError(ctx, "invalid team ID", err)

			return
		}

		for _, t := range teams {
			if t.Team.ID == teamUUID {
				team = &t.Team
				break
			}
		}

		if team == nil {
			a.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

			telemetry.ReportError(ctx, "team not found", err)

			return
		}
	} else {
		for _, t := range teams {
			if t.UsersTeam.IsDefault {
				team = &t.Team
				break
			}
		}

		if team == nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Default team not found")

			telemetry.ReportError(ctx, "default team not found", err)

			return
		}
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		telemetry.WithTeamID(team.ID.String()),
	)

	envs, err := a.db.GetEnvs(ctx, team.ID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting sandbox templates")

		telemetry.ReportCriticalError(ctx, "error when getting envs", err)

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
			DiskSizeMB:    int32(item.DiskMB),
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
