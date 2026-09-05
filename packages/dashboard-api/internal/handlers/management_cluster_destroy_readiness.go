package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *APIStore) ManagementClusterDestroyReadiness(c *gin.Context, clusterID api.ClusterID) {
	ctx := c.Request.Context()
	active, err := s.db.Dashboard.ClusterHasActiveEnvironments(ctx, clusterID)
	if err != nil {
		logger.L().Error(ctx, "Failed to check cluster destroy readiness", zap.Error(err), logger.WithClusterID(clusterID))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to check cluster destroy readiness")

		return
	}
	if active {
		s.sendAPIStoreError(c, http.StatusConflict, "Cluster still has active templates or snapshots. Delete them before destroying the deployment.")

		return
	}

	c.Status(http.StatusNoContent)
}
