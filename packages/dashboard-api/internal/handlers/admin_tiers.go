package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetAdminTiers(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list tiers")

	rows, err := s.db.Dashboard.ListTiers(ctx)
	if err != nil {
		logger.L().Error(ctx, "failed to list tiers", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to list tiers")

		return
	}

	tiers := make([]api.AdminTier, 0, len(rows))
	for _, row := range rows {
		tiers = append(tiers, api.AdminTier{
			Id:   row.ID,
			Name: row.Name,
		})
	}

	c.JSON(http.StatusOK, api.AdminTiersResponse{
		Tiers: tiers,
	})
}
