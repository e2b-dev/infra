package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/dberrors"
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
		a.sendAPIStoreError(c, ctx, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	telemetry.SetAttributesWithGin(c, ctx,
		telemetry.WithTeamID(team.ID.String()),
	)

	template, err := a.sqlcDB.GetTemplateByIDWithAliases(ctx, templateID)
	switch {
	case dberrors.IsNotFoundError(err):
		a.sendAPIStoreError(c, ctx, http.StatusNotFound, fmt.Sprintf("Template %s not found", templateID), err)

		return
	case err != nil:
		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error when getting template", err)

		return
	case template.TeamID != team.ID:
		a.sendAPIStoreError(c, ctx, http.StatusForbidden, fmt.Sprintf("You don't have access to this sandbox template (%s)", templateID), err)

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
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, "Invalid next token", err)

		return
	}

	builds, err := a.sqlcDB.GetTemplateBuilds(ctx, queries.GetTemplateBuildsParams{
		TemplateID: templateID,
		CursorTime: pagination.CursorTime(),
		CursorID:   pagination.CursorID(),
		BuildLimit: pagination.QueryLimit(),
	})
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error when getting builds", err)

		return
	}

	builds = pagination.ProcessResultsWithHeader(c, builds, func(b queries.EnvBuild) (time.Time, string) {
		return b.CreatedAt, b.ID.String()
	})

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
			DiskSizeMB:  sharedUtils.CastPtr(item.TotalDiskSizeMb, func(v int64) api.DiskSizeMB { return api.DiskSizeMB(v) }),
			EnvdVersion: item.EnvdVersion,
		})
	}

	c.JSON(http.StatusOK, res)
}
