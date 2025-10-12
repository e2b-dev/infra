package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// GetTemplates serves to list templates (e.g. in CLI)
func (a *APIStore) GetTemplates(c *gin.Context, params api.GetTemplatesParams) {
	ctx := c.Request.Context()

	team, _, apiErr := a.GetTeamAndTier(c, params.TeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)
		return
	}

	if params.TeamID != nil {
		teamUUID, err := uuid.Parse(*params.TeamID)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid team ID")

			telemetry.ReportError(ctx, "invalid team ID", err)

			return
		}

		if team.ID != teamUUID {
			a.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

			telemetry.ReportError(ctx, "team not found", err)

			return
		}
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
	)

	envs, err := a.sqlcDB.GetTeamTemplates(ctx, team.ID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting templates")
		telemetry.ReportCriticalError(ctx, "error when getting templates", err)
		return
	}

	telemetry.ReportEvent(ctx, "listed environments")

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "listed environments", properties)

	templates := make([]*api.Template, 0, len(envs))
	for _, item := range envs {
		var createdBy *api.TeamUser
		if item.CreatorEmail != nil && item.CreatorID != nil {
			createdBy = &api.TeamUser{
				Id:    *item.CreatorID,
				Email: *item.CreatorEmail,
			}
		}

		envdVersion := ""
		if item.BuildEnvdVersion != nil {
			envdVersion = *item.BuildEnvdVersion
		} else {
			zap.L().Error("failed to determine envd version", logger.WithTemplateID(item.Env.ID))
		}

		diskMB := int64(0)
		if item.BuildTotalDiskSizeMb != nil {
			diskMB = *item.BuildTotalDiskSizeMb
		}

		templates = append(templates, &api.Template{
			TemplateID:    item.Env.ID,
			BuildID:       item.BuildID.String(),
			CpuCount:      int32(item.BuildVcpu),
			MemoryMB:      int32(item.BuildRamMb),
			DiskSizeMB:    int32(diskMB),
			Public:        item.Env.Public,
			Aliases:       item.Aliases,
			CreatedAt:     item.Env.CreatedAt,
			UpdatedAt:     item.Env.UpdatedAt,
			LastSpawnedAt: item.Env.LastSpawnedAt,
			SpawnCount:    item.Env.SpawnCount,
			BuildCount:    item.Env.BuildCount,
			CreatedBy:     createdBy,
			EnvdVersion:   envdVersion,
		})
	}

	c.JSON(http.StatusOK, templates)
}
