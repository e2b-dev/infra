package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetTemplatesDefaults(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list default templates")

	rows, err := s.db.GetDefaultTemplates(ctx)
	if err != nil {
		logger.L().Error(ctx, "failed to get default templates", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get default templates")

		return
	}

	if len(rows) == 0 {
		c.JSON(http.StatusOK, api.DefaultTemplatesResponse{
			Templates: []api.DefaultTemplate{},
		})

		return
	}

	envIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		envIDs = append(envIDs, row.TemplateID)
	}

	aliasRows, err := s.db.GetTemplateAliases(ctx, envIDs)
	if err != nil {
		logger.L().Error(ctx, "failed to get default template aliases", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get default template aliases")

		return
	}

	aliasesByEnv := make(map[string][]api.DefaultTemplateAlias, len(rows))
	for _, a := range aliasRows {
		aliasesByEnv[a.EnvID] = append(aliasesByEnv[a.EnvID], api.DefaultTemplateAlias{
			Alias:     a.Alias,
			Namespace: a.Namespace,
		})
	}

	templates := make([]api.DefaultTemplate, 0, len(rows))
	for _, row := range rows {
		aliases := aliasesByEnv[row.TemplateID]
		if aliases == nil {
			aliases = []api.DefaultTemplateAlias{}
		}

		templates = append(templates, api.DefaultTemplate{
			Id:              row.TemplateID,
			Aliases:         aliases,
			BuildId:         row.BuildID,
			RamMb:           row.RamMb,
			Vcpu:            row.Vcpu,
			TotalDiskSizeMb: row.TotalDiskSizeMb,
			EnvdVersion:     row.EnvdVersion,
			CreatedAt:       row.CreatedAt,
			Public:          row.Public,
			BuildCount:      row.BuildCount,
			SpawnCount:      row.SpawnCount,
		})
	}

	c.JSON(http.StatusOK, api.DefaultTemplatesResponse{
		Templates: templates,
	})
}
