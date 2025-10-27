package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (a *APIStore) GetTemplatesTemplateID(c *gin.Context, templateID api.TemplateID) {
	ctx := c.Request.Context()

	team, apiErr := a.GetTeamAndLimits(c, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)

		return
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
	)

	template, err := a.sqlcDB.GetTemplateByIDWithAliases(ctx, templateID)
	switch {
	case dberrors.IsNotFoundError(err):
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template %s not found", templateID))
		telemetry.ReportError(ctx, "template not found", err, telemetry.WithTemplateID(templateID))

		return
	case err != nil:
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template")
		telemetry.ReportCriticalError(ctx, "error when getting template", err)

		return
	case template.TeamID != team.ID:
		telemetry.ReportError(ctx, "user doesn't have access to the template", nil, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to this sandbox template (%s)", templateID))

		return
	}

	builds, err := a.sqlcDB.GetTemplateBuilds(ctx, templateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting builds")
		telemetry.ReportCriticalError(ctx, "error when getting builds", err)

		return
	}

	res := api.TemplateWithBuilds{
		TemplateID:    template.ID,
		Public:        template.Public,
		Aliases:       template.Aliases,
		CreatedAt:     template.CreatedAt,
		UpdatedAt:     template.UpdatedAt,
		LastSpawnedAt: template.LastSpawnedAt,
		SpawnCount:    template.SpawnCount,
		Builds:        make([]api.TemplateBuild, 0, len(builds)),
	}

	for _, item := range builds {
		res.Builds = append(res.Builds, api.TemplateBuild{
			BuildID:     item.ID,
			Status:      api.TemplateBuildStatus(item.Status),
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
			FinishedAt:  item.FinishedAt,
			CpuCount:    api.CPUCount(item.Vcpu),
			MemoryMB:    api.MemoryMB(item.RamMb),
			DiskSizeMB:  utils.CastPtr(item.TotalDiskSizeMb, func(v int64) api.DiskSizeMB { return api.DiskSizeMB(v) }),
			EnvdVersion: item.EnvdVersion,
		})
	}

	c.JSON(http.StatusOK, res)
}
