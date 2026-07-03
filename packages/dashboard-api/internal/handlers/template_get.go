package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

	resp := api.TemplateDetail{
		TemplateID:    row.ActiveEnv.ID,
		BuildID:       row.BuildID.String(),
		Public:        row.ActiveEnv.Public,
		Aliases:       row.Aliases,
		Names:         row.Names,
		CreatedAt:     row.ActiveEnv.CreatedAt,
		UpdatedAt:     row.ActiveEnv.UpdatedAt,
		LastSpawnedAt: row.ActiveEnv.LastSpawnedAt,
		SpawnCount:    row.ActiveEnv.SpawnCount,
		BuildCount:    row.ActiveEnv.BuildCount,
	}

	if row.BuildID != uuid.Nil {
		cpuCount := row.BuildVcpu
		memoryMB := row.BuildRamMb
		resp.CpuCount = &cpuCount
		resp.MemoryMB = &memoryMB
		resp.DiskSizeMB = row.BuildTotalDiskSizeMb
		resp.EnvdVersion = row.BuildEnvdVersion
	}

	c.JSON(http.StatusOK, resp)
}
