package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetTemplatesTemplateID(c *gin.Context, templateID api.TemplateID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "get template")

	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(teamID.String()),
		telemetry.WithTemplateID(templateID),
	)

	row, err := s.db.GetTeamTemplate(ctx, queries.GetTeamTemplateParams{
		ID:     templateID,
		TeamID: teamID,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Template not found")

			return
		}

		logger.L().Error(ctx, "Error getting template",
			zap.Error(err),
			logger.WithTeamID(teamID.String()),
			logger.WithTemplateID(templateID),
		)
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template")

		return
	}

	diskSizeMB := int64(0)
	if row.BuildTotalDiskSizeMb != nil {
		diskSizeMB = *row.BuildTotalDiskSizeMb
	}

	envdVersion := ""
	if row.BuildEnvdVersion != nil {
		envdVersion = *row.BuildEnvdVersion
	}

	c.JSON(http.StatusOK, api.TemplateDetail{
		TemplateID:    row.Env.ID,
		BuildID:       row.BuildID.String(),
		CpuCount:      api.CPUCount(row.BuildVcpu),
		MemoryMB:      api.MemoryMB(row.BuildRamMb),
		DiskSizeMB:    api.DiskSizeMB(diskSizeMB),
		Public:        row.Env.Public,
		Aliases:       row.Aliases,
		Names:         row.Names,
		CreatedAt:     row.Env.CreatedAt,
		UpdatedAt:     row.Env.UpdatedAt,
		LastSpawnedAt: row.Env.LastSpawnedAt,
		SpawnCount:    row.Env.SpawnCount,
		BuildCount:    row.Env.BuildCount,
		EnvdVersion:   envdVersion,
	})
}
