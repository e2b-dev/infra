package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	templateBuildsDefaultLimit = int32(100)
	templateBuildsMaxLimit     = int32(100)
)

var maxBuildID = uuid.Max.String()

func (a *APIStore) GetTemplatesTemplateID(c *gin.Context, templateID api.TemplateID, params api.GetTemplatesTemplateIDParams) {
	ctx := c.Request.Context()

	team, apiErr := a.GetTeam(ctx, c, nil)
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

	// Initialize pagination
	pagination, err := utils.NewPagination[queries.EnvBuild](
		utils.PaginationParams{
			Limit:     params.Limit,
			NextToken: params.NextToken,
		},
		utils.PaginationConfig{
			DefaultLimit: templateBuildsDefaultLimit,
			MaxLimit:     templateBuildsMaxLimit,
			DefaultID:    maxBuildID,
		},
	)
	if err != nil {
		telemetry.ReportError(ctx, "error parsing pagination cursor", err)
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid next token")

		return
	}

	builds, err := a.sqlcDB.GetTemplateBuilds(ctx, queries.GetTemplateBuildsParams{
		TemplateID: templateID,
		CursorTime: pagination.CursorTime(),
		CursorID:   pagination.CursorID(),
		BuildLimit: pagination.QueryLimit(),
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting builds")
		telemetry.ReportCriticalError(ctx, "error when getting builds", err)

		return
	}

	builds = pagination.ProcessResultsWithHeader(c, builds, func(b queries.EnvBuild) (time.Time, string) {
		return b.CreatedAt, b.ID.String()
	})

	res := api.TemplateWithBuilds{
		TemplateID:    template.ID,
		Public:        template.Public,
		Aliases:       template.Aliases,
		Names:         template.Names,
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
			DiskSizeMB:  sharedUtils.CastPtr(item.TotalDiskSizeMb, func(v int64) api.DiskSizeMB { return api.DiskSizeMB(v) }),
			EnvdVersion: item.EnvdVersion,
		})
	}

	c.JSON(http.StatusOK, res)
}
